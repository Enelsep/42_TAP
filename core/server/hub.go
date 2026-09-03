package server

import (
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Enelsep/42_TAP/core/world"
)

// Client is one connected player: the socket, and a buffered outbound queue
// drained by its own writer goroutine, so a slow or dead client can never
// block a broadcast to everyone else.
type Client struct {
	conn      net.Conn
	out       chan string
	name      string          // empty until CONNECT succeeds
	room      string          // canonical room id; only meaningful once name != ""
	group     string          // group id; empty means "not in a group"
	inventory map[string]bool // canonical item ids currently held
}

func newClient(conn net.Conn) *Client {
	return &Client{conn: conn, out: make(chan string, 64), inventory: map[string]bool{}}
}

// send enqueues line without blocking. If the client's buffer is full, the
// line is dropped rather than stalling whoever is broadcasting.
func (c *Client) send(line string) {
	select {
	case c.out <- line:
	default:
	}
}

// writeLoop drains c.out to the socket until the channel is closed by the
// hub (on Unregister) or by handleConn (if CONNECT never succeeded), then
// closes the connection itself — always after its last Write, never racing
// against one.
func (c *Client) writeLoop() {
	defer c.conn.Close()
	for line := range c.out {
		c.conn.Write([]byte(line))
	}
}

// Hub is the mutex-guarded registry of connected, named clients, the groups
// they've formed, and the dynamic (post-startup) item placement — the whole
// of the game's mutable state behind one lock, per the roadmap's concurrency
// model.
type Hub struct {
	mu        sync.Mutex
	clients   map[string]*Client
	groups    map[string]map[string]*Client // group id -> members, by name
	roomItems map[string][]string           // room id -> item ids on the floor
	world     *world.World                  // read-only: item names for display-name resolution
}

// NewHub seeds the dynamic floor-item state from w's static placement.
func NewHub(w *world.World) *Hub {
	h := &Hub{
		clients:   make(map[string]*Client),
		groups:    make(map[string]map[string]*Client),
		roomItems: make(map[string][]string),
		world:     w,
	}
	for id, loc := range w.Locations {
		if len(loc.Items) > 0 {
			h.roomItems[id] = slices.Clone(loc.Items)
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

// Unregister removes c from every index that can reach it — the client
// registry and, if it was in one, its group — then closes its outbound
// channel, which stops its writeLoop goroutine. A client left behind in any
// index is a ghost: the next broadcast to it sends on a closed channel and
// panics the whole process, not just that connection.
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
	close(c.out)
}

// Broadcast enqueues line to every registered client.
func (h *Hub) Broadcast(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		c.send(line)
	}
}

// BroadcastRoom enqueues line to every registered client currently in room,
// skipping except when it is non-nil (typically the client who caused the
// event, who already got a direct reply).
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

// SetRoom moves c to room. Every write to c.room must go through here (never
// c.room = ... directly) once c is registered, so a concurrent BroadcastRoom
// or PlayersIn reading c.room under h.mu never races the write.
func (h *Hub) SetRoom(c *Client, room string) {
	h.mu.Lock()
	defer h.mu.Unlock()
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

// CreateGroup makes c the sole member of a fresh group and returns its id.
// The id is c's own name, disambiguated with a numeric suffix if a
// still-populated group already claims it — possible if c left a group that
// other members kept alive (GroupNotFound/D12 is the mirror case).
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

// JoinGroup resolves arg as a group id, falling back to the current group of
// the player named arg (D12/quirk #4: GROUP INVITE's event carries only the
// inviter's name, so an invitee has no id to pass — this lets
// "GROUP JOIN <inviter>" work anyway). ok is false if neither resolves.
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
// moves it into c's inventory. The whole resolve-then-move happens under one
// lock acquisition, so two players racing the same TAKE can never both
// succeed — items are unique instances (RFC §8) by construction, not by luck.
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
