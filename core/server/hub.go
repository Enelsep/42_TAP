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

	// quests holds only "active" and "completed" entries (protocol.QuestActive
	// / protocol.QuestCompleted): a quest a player has never accepted is simply
	// absent, which is what D10 calls the implicit "available" state.
	quests map[string]string

	hp        int  // current HP; see PlayerMaxHP/RespawnHP in combat.go (D15)
	defending bool // DEFEND armed: halves the damage of the next hit taken

	rate commandRate // flood tracking (D17); touched only by this client's reader goroutine
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

// logReply logs the OK/ERR outcome of one reply, the moment it is handed to
// send — every handler's outcome ends up here without threading a logger
// through all of them (D17). Every broadcast helper (Broadcast, BroadcastRoom,
// BroadcastGroup, SendTo) also funnels through send, but an EVT line matches
// neither prefix and is skipped: it is a notification about someone else's
// action, not a reply to this client's own command, and logging it here
// would misattribute it.
func logReply(player, line string) {
	head, rest, _ := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
	switch head {
	case "OK":
		slog.Info("reply", "player", player, "status", "ok", "data", rest)
	case "ERR":
		code, symbol, _ := strings.Cut(rest, " ")
		slog.Info("reply", "player", player, "status", "err", "code", code, "symbol", symbol)
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
	mu           sync.Mutex
	clients      map[string]*Client
	groups       map[string]map[string]*Client // group id -> members, by name
	roomItems    map[string][]string           // room id -> item ids on the floor
	npcTalk      map[string]int                // npc id -> next dialogue index (D14: one shared cursor)
	grantedItems map[string]bool               // quest grant/reward item ids already created once, see grantOnceLocked
	npcHP        map[string]int                // npc id -> current hp, enemies only (D15); 0 = dead, gone for good
	world        *world.World                  // read-only: item names for display-name resolution
}

// NewHub seeds the dynamic floor-item state from w's static placement.
func NewHub(w *world.World) *Hub {
	h := &Hub{
		clients:      make(map[string]*Client),
		groups:       make(map[string]map[string]*Client),
		roomItems:    make(map[string][]string),
		npcTalk:      make(map[string]int),
		grantedItems: make(map[string]bool),
		npcHP:        make(map[string]int),
		world:        w,
	}
	for id, loc := range w.Locations {
		if len(loc.Items) > 0 {
			h.roomItems[id] = slices.Clone(loc.Items)
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

// Unregister removes c from every index that can reach it — the client
// registry and, if it was in one, its group — then closes its outbound
// channel, which stops its writeLoop goroutine. A client left behind in any
// index is a ghost: the next broadcast to it sends on a closed channel and
// panics the whole process, not just that connection.
//
// c's inventory is dropped onto its current room's floor first. Without
// this a disconnect destroyed whatever c was carrying — for the hunter's
// key, the only way into bossroom and with no other source once the hunter
// is dead (D15), that meant one QUIT could lock the room for good.
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
	close(c.out)
}

// grantOnceLocked creates itemID into c's inventory the first time it is
// requested for a quest grant or reward, and does nothing on every later
// call for the same id — item ids are unique instances (RFC §8), the same
// invariant TakeItem already relies on, and quest grants/rewards were the
// one path that instead handed a fresh copy to every simultaneous holder.
// h.mu must already be held.
func (h *Hub) grantOnceLocked(c *Client, itemID string) {
	if h.grantedItems[itemID] {
		return
	}
	h.grantedItems[itemID] = true
	c.inventory[itemID] = true
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

// SetRoom moves c to room. Every write to c.room must go through setRoomLocked
// (never c.room = ... directly) once c is registered, so a concurrent
// BroadcastRoom or PlayersIn reading c.room under h.mu never races the write.
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
