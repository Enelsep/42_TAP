package server

import (
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Enelsep/42_TAP/core/world"
)

type Client struct {
	conn      net.Conn
	out       chan string
	name      string
	room      string
	group     string
	inventory map[string]bool
	quests    map[string]string

	hp        int
	defending bool

	rate commandRate
}

func newClient(conn net.Conn) *Client {
	return &Client{
		conn:      conn,
		out:       make(chan string, 64),
		inventory: map[string]bool{},
		quests:    map[string]string{},
		hp:        PlayerMaxHP,
	}
}

// send enqueues line without blocking. If the client's buffer is full, the
// line is dropped rather than stalling whoever is broadcasting.
func (c *Client) send(line string) {
	logReply(c.name, line)
	select {
	case c.out <- line:
	default:
	}
}

const maxLoggedReplyData = 200

func logReply(player, line string) {
	head, rest, _ := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
	switch head {
	case "OK":
		if len(rest) > maxLoggedReplyData {
			slog.Info("reply", "player", player, "status", "ok",
				"data", rest[:maxLoggedReplyData]+"…(truncated)", "data_len", len(rest))
		} else {
			slog.Info("reply", "player", player, "status", "ok", "data", rest)
		}
	case "ERR":
		code, symbol, _ := strings.Cut(rest, " ")
		slog.Info("reply", "player", player, "status", "err", "code", code, "symbol", symbol)
	}
}

func (c *Client) writeLoop() {
	defer c.conn.Close()
	for line := range c.out {
		c.conn.Write([]byte(line))
	}
}

type Hub struct {
	mu           sync.Mutex
	clients      map[string]*Client
	groups       map[string]map[string]*Client
	roomItems    map[string][]string
	npcTalk      map[string]int
	spawnedItems map[string]bool
	npcHP        map[string]int
	world        *world.World
}

// NewHub seeds the dynamic floor-item state from w's static placement.
func NewHub(w *world.World) *Hub {
	h := &Hub{
		clients:      make(map[string]*Client),
		groups:       make(map[string]map[string]*Client),
		roomItems:    make(map[string][]string),
		npcTalk:      make(map[string]int),
		spawnedItems: make(map[string]bool),
		npcHP:        make(map[string]int),
		world:        w,
	}
	for id, loc := range w.Locations {
		if len(loc.Items) > 0 {
			h.roomItems[id] = slices.Clone(loc.Items)
			for _, item := range loc.Items {
				h.spawnedItems[item] = true
			}
		}
	}
	for id, npc := range w.NPCs {
		if npc.Role == world.RoleEnemy {
			h.npcHP[id] = npc.Stats.HP
		}
	}
	return h
}

// Register adds c under c.name. It refuses and returns false if that name
// is already taken (RFC 201 NAME_IN_USE).
func (h *Hub) Register(c *Client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, taken := h.clients[c.name]; taken {
		return false
	}
	h.clients[c.name] = c
	return true
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[c.name] != c {
		return
	}
	delete(h.clients, c.name)
	if g := h.groups[c.group]; g != nil {
		delete(g, c.name)
		if len(g) == 0 {
			delete(h.groups, c.group)
		}
	}
	c.group = ""
	for id := range c.inventory {
		h.roomItems[c.room] = append(h.roomItems[c.room], id)
	}
	c.inventory = map[string]bool{} // the floor owns them now
	close(c.out)
}

func (h *Hub) spawnLocked(c *Client, itemID string) {
	if itemID == "" || h.spawnedItems[itemID] {
		return
	}
	h.spawnedItems[itemID] = true
	c.inventory[itemID] = true
}

func (h *Hub) consumeLocked(c *Client, itemID string) {
	delete(c.inventory, itemID)
	delete(h.spawnedItems, itemID)
}

// Broadcast enqueues line to every registered client.
func (h *Hub) Broadcast(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		c.send(line)
	}
}

func (h *Hub) BroadcastRoom(room, line string, except *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		if c.room == room && c != except {
			c.send(line)
		}
	}
}

// Count reports how many clients are currently registered.
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// PlayersIn returns the names of every client currently in room, sorted.
func (h *Hub) PlayersIn(room string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	players := []string{}
	for name, c := range h.clients {
		if c.room == room {
			players = append(players, name)
		}
	}
	slices.Sort(players)
	return players
}

func (h *Hub) SetRoom(c *Client, room string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.setRoomLocked(c, room)
}

// setRoomLocked is SetRoom's body, for callers (combat.go's respawn and
// flee paths) that already hold h.mu and would deadlock calling SetRoom.
func (h *Hub) setRoomLocked(c *Client, room string) {
	c.room = room
}

// SendTo enqueues line to the named client, if one is currently registered.
// This — never handing out a *Client to use unlocked — is what keeps a send
// from racing a concurrent Unregister's close(c.out), which would panic.
func (h *Hub) SendTo(name, line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.clients[name]; ok {
		c.send(line)
	}
}

func (h *Hub) CreateGroup(c *Client) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := c.name
	for n := 2; h.groups[id] != nil; n++ {
		id = c.name + "-" + strconv.Itoa(n)
	}
	h.groups[id] = map[string]*Client{c.name: c}
	c.group = id
	return id
}

func (h *Hub) JoinGroup(c *Client, arg string) (id string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id = arg
	if h.groups[id] == nil {
		if target := h.clients[arg]; target != nil && target.group != "" {
			id = target.group
		}
	}
	if h.groups[id] == nil {
		return "", false
	}
	h.groups[id][c.name] = c
	c.group = id
	return id, true
}

// LeaveGroup removes c from its group, deleting the group once it's empty,
// and returns the id c was in.
func (h *Hub) LeaveGroup(c *Client) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := c.group
	delete(h.groups[id], c.name)
	if len(h.groups[id]) == 0 {
		delete(h.groups, id)
	}
	c.group = ""
	return id
}

// BroadcastGroup enqueues line to every member of group, skipping except if
// it is non-nil.
func (h *Hub) BroadcastGroup(group, line string, except *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.groups[group] {
		if c != except {
			c.send(line)
		}
	}
}

// RoomItems returns the canonical ids of every item currently on room's
// floor, sorted.
func (h *Hub) RoomItems(room string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	items := slices.Clone(h.roomItems[room])
	slices.Sort(items)
	return items
}

// Holds reports whether c currently carries item — used by MOVE to check a
// gated exit's Requires entry.
func (h *Hub) Holds(c *Client, item string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return c.inventory[item]
}

// Inventory returns the canonical ids c is carrying, sorted.
func (h *Hub) Inventory(c *Client) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	items := make([]string, 0, len(c.inventory))
	for id := range c.inventory {
		items = append(items, id)
	}
	slices.Sort(items)
	return items
}

// findItem returns the index in items whose canonical id equals arg exactly,
// or — failing that — whose display name matches arg case-insensitively
// (RFC §8.3/8.4). -1 if neither matches.
func findItem(items []string, arg string, w *world.World) int {
	for i, id := range items {
		if id == arg {
			return i
		}
	}
	for i, id := range items {
		if item := w.Items[id]; item != nil && strings.EqualFold(item.Name, arg) {
			return i
		}
	}
	return -1
}

// TakeItem resolves arg against room's floor items (id or display name) and
// moves it into c's inventory. Resolve-then-move happens under one lock, so
// two players racing the same TAKE can never both succeed.
func (h *Hub) TakeItem(c *Client, room, arg string) (id string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	items := h.roomItems[room]
	i := findItem(items, arg, h.world)
	if i == -1 {
		return "", false
	}
	id = items[i]
	h.roomItems[room] = append(items[:i], items[i+1:]...)
	c.inventory[id] = true
	return id, true
}

// DropItem resolves arg against c's inventory (id or display name) and moves
// it onto room's floor.
func (h *Hub) DropItem(c *Client, room, arg string) (id string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	id = arg
	if !c.inventory[id] {
		id = ""
		for held := range c.inventory {
			if item := h.world.Items[held]; item != nil && strings.EqualFold(item.Name, arg) {
				id = held
				break
			}
		}
		if id == "" {
			return "", false
		}
	}
	delete(c.inventory, id)
	h.roomItems[room] = append(h.roomItems[room], id)
	return id, true
}
