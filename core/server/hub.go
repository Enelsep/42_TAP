package server

import (
	"net"
	"slices"
	"sync"
)

// Client is one connected player: the socket, and a buffered outbound queue
// drained by its own writer goroutine, so a slow or dead client can never
// block a broadcast to everyone else.
type Client struct {
	conn net.Conn
	out  chan string
	name string // empty until CONNECT succeeds
	room string // canonical room id; only meaningful once name != ""
}

func newClient(conn net.Conn) *Client {
	return &Client{conn: conn, out: make(chan string, 64)}
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

// Hub is the mutex-guarded registry of connected, named clients.
type Hub struct {
	mu      sync.Mutex
	clients map[string]*Client
}

func NewHub() *Hub {
	return &Hub{clients: make(map[string]*Client)}
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

// Unregister removes c and closes its outbound channel, which stops its
// writeLoop goroutine.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[c.name] == c {
		delete(h.clients, c.name)
		close(c.out)
	}
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
// skipping except if it is non-nil (typically the client who caused the
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
