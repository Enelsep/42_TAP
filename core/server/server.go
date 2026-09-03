package server

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"strconv"
	"strings"

	"github.com/Enelsep/42_TAP/core/protocol"
	"github.com/Enelsep/42_TAP/core/world"
)

// Server owns the listening socket, the loaded world, and the hub of
// connected clients.
type Server struct {
	addr  string
	world *world.World
	hub   *Hub
}

// New creates a Server that will listen on addr (e.g. ":4242") and place new
// players in w's start room.
func New(addr string, w *world.World) *Server {
	return &Server{addr: addr, world: w, hub: NewHub()}
}

// Run listens on s.addr and blocks, accepting connections until the listener
// fails. Each connection is handled on its own goroutine.
func (s *Server) Run() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	log.Printf("tap server: listening on %s", s.addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleConn(conn)
	}
}

// handleConn owns one client connection end to end: greeting, a read loop
// that parses and dispatches each line, and cleanup on the way out.
func (s *Server) handleConn(conn net.Conn) {
	remote := conn.RemoteAddr()
	log.Printf("tap server: connected %s", remote)

	c := newClient(conn)
	go c.writeLoop()

	defer func() {
		// Closing conn is writeLoop's job (after it drains c.out) so a
		// reply queued right before disconnect is never lost to a race.
		if c.name != "" {
			// Remove state, *then* tell the room, *then* the whole server —
			// the order the subject requires for a clean disconnect.
			room := c.room
			s.hub.Unregister(c)
			s.hub.BroadcastRoom(room, protocol.FormatEvent(protocol.Event{
				Scope:    protocol.EvtRoom,
				Kind:     protocol.KindPresence,
				Presence: protocol.PresenceLeave,
				Player:   c.name,
			}), nil)
			s.broadcastStats()
		} else {
			close(c.out)
		}
		log.Printf("tap server: disconnected %s", remote)
	}()

	c.send(protocol.Greeting + protocol.LineTerm)

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()

		cmd, err := protocol.ParseCommand(line)
		if err != nil {
			c.send(protocol.FormatErr(protocol.ErrBadRequest))
			continue
		}

		// AUTHENTICATED gate (§3.3): no gameplay before a successful CONNECT.
		if c.name == "" && cmd.Verb != protocol.VerbConnect && cmd.Verb != protocol.VerbQuit {
			c.send(protocol.FormatErr(protocol.ErrNotConnected))
			continue
		}

		switch cmd.Verb {
		case protocol.VerbConnect:
			s.handleConnect(c, cmd)

		case protocol.VerbQuit:
			c.send(protocol.FormatOK("bye"))
			return // runs the deferred cleanup above

		case protocol.VerbWho:
			c.send(protocol.FormatOK("players=" + strconv.Itoa(s.hub.Count())))

		case protocol.VerbLook:
			s.handleLook(c)

		case protocol.VerbMove:
			s.handleMove(c, cmd)

		case protocol.VerbChat:
			s.handleChat(c, cmd)

		case protocol.VerbGroup:
			s.handleGroup(c, cmd)

		default:
			log.Printf("tap server: %s -> %+v", remote, cmd)
			c.send(protocol.FormatOK(""))
		}
	}
}

// handleConnect claims a name in the hub or replies 201 NAME_IN_USE, then
// places the new player in the world's start room.
func (s *Server) handleConnect(c *Client, cmd protocol.Command) {
	if c.name != "" {
		c.send(protocol.FormatErr(protocol.ErrNameInUse))
		return
	}
	// Set both fields *before* Register publishes c into the hub map, so no
	// other goroutine can ever observe c with a name but no room yet.
	c.name = cmd.Arg
	c.room = s.world.Start
	if !s.hub.Register(c) {
		c.name = ""
		c.room = ""
		c.send(protocol.FormatErr(protocol.ErrNameInUse))
		return
	}
	log.Printf("tap server: %s is now %s in %s", c.conn.RemoteAddr(), c.name, c.room)
	c.send(protocol.FormatOK("connected"))
	s.hub.BroadcastRoom(c.room, protocol.FormatEvent(protocol.Event{
		Scope:    protocol.EvtRoom,
		Kind:     protocol.KindPresence,
		Presence: protocol.PresenceEnter,
		Player:   c.name,
	}), c)
	s.broadcastStats()
}

// handleLook replies with the current room, who else is there, and what's
// on the floor. The world's static item/NPC placement is used as-is for now
// — dynamic per-room item state (what TAKE/DROP actually mutate) arrives
// with T3.5.
func (s *Server) handleLook(c *Client) {
	loc := s.world.Locations[c.room]

	reply := protocol.LookReply{
		Room: protocol.Room{
			ID:          loc.ID,
			Name:        loc.Name,
			Description: loc.Description,
			Exits:       loc.Exits,
		},
		Players: s.hub.PlayersIn(c.room),
		Items:   nonNil(loc.Items),
		NPCs:    []string{},
	}
	if loc.Spawns != nil {
		reply.NPCs = []string{loc.Spawns.NPCType}
	}

	data, err := json.Marshal(reply)
	if err != nil {
		log.Printf("tap server: marshal LookReply for %s: %v", c.name, err)
		return
	}
	c.send(protocol.FormatOK(string(data)))
}

// handleMove validates the exit, moves c under the hub's lock, and announces
// the swap to both rooms — in the order the RFC example shows: reply to the
// mover first, then LEAVE the old room, then ENTER the new one.
func (s *Server) handleMove(c *Client, cmd protocol.Command) {
	loc := s.world.Locations[c.room]
	dir := strings.ToLower(strings.TrimSpace(cmd.Arg))

	target, ok := loc.Exits[dir]
	if !ok {
		c.send(protocol.FormatErr(protocol.ErrNoExit))
		return
	}
	if _, gated := loc.Requires[dir]; gated {
		// TODO(T3.5): let this through once inventory exists and c carries
		// the required item. Until then the exit stays closed to everyone.
		c.send(protocol.FormatErr(protocol.ErrNoExit))
		return
	}

	old := c.room
	s.hub.SetRoom(c, target)
	c.send(protocol.FormatOK("room=" + target))
	s.hub.BroadcastRoom(old, protocol.FormatEvent(protocol.Event{
		Scope:    protocol.EvtRoom,
		Kind:     protocol.KindPresence,
		Presence: protocol.PresenceLeave,
		Player:   c.name,
	}), nil)
	s.hub.BroadcastRoom(target, protocol.FormatEvent(protocol.Event{
		Scope:    protocol.EvtRoom,
		Kind:     protocol.KindPresence,
		Presence: protocol.PresenceEnter,
		Player:   c.name,
	}), c)
}

// handleChat replies OK, then broadcasts the corresponding EVT *CHAT to the
// requested scope — sender included, per the RFC's own example transcript.
func (s *Server) handleChat(c *Client, cmd protocol.Command) {
	c.send(protocol.FormatOK(""))

	var scope protocol.EventScope
	switch cmd.Scope {
	case protocol.ChatGlobal:
		scope = protocol.EvtGlobal
	case protocol.ChatRoom:
		scope = protocol.EvtRoom
	case protocol.ChatGroup:
		scope = protocol.EvtGroup
	}
	line := protocol.FormatEvent(protocol.Event{
		Scope:   scope,
		Kind:    protocol.KindChat,
		Player:  c.name,
		Message: cmd.Arg,
	})

	switch cmd.Scope {
	case protocol.ChatGlobal:
		s.hub.Broadcast(line)
	case protocol.ChatRoom:
		s.hub.BroadcastRoom(c.room, line, nil)
	case protocol.ChatGroup:
		// No group to speak to is not a protocol error (the RFC defines none
		// for it): same as talking in an empty room, the OK stands and
		// nothing goes out. See D12.
		if c.group != "" {
			s.hub.BroadcastGroup(c.group, line, nil)
		}
	}
}

// handleGroup dispatches the GROUP subcommands. See D12 for the id scheme
// and the two RFC-silent edge cases (GROUP INVITE to an offline/unknown
// player, GROUP JOIN with nothing to resolve to).
func (s *Server) handleGroup(c *Client, cmd protocol.Command) {
	switch cmd.Sub {
	case protocol.GroupCreate:
		if c.group != "" {
			c.send(protocol.FormatErr(protocol.ErrAlreadyInGroup))
			return
		}
		id := s.hub.CreateGroup(c)
		c.send(protocol.FormatOK("group=" + id))

	case protocol.GroupJoin:
		if c.group != "" {
			c.send(protocol.FormatErr(protocol.ErrAlreadyInGroup))
			return
		}
		id, ok := s.hub.JoinGroup(c, cmd.Arg)
		if !ok {
			c.send(protocol.FormatErr(protocol.ErrGroupNotFound))
			return
		}
		c.send(protocol.FormatOK("group=" + id))
		s.hub.BroadcastGroup(id, protocol.FormatEvent(protocol.Event{
			Scope: protocol.EvtGroup, Kind: protocol.KindJoin, Player: c.name,
		}), c)

	case protocol.GroupLeave:
		if c.group == "" {
			c.send(protocol.FormatErr(protocol.ErrNotInGroup))
			return
		}
		id := s.hub.LeaveGroup(c)
		c.send(protocol.FormatOK(""))
		s.hub.BroadcastGroup(id, protocol.FormatEvent(protocol.Event{
			Scope: protocol.EvtGroup, Kind: protocol.KindLeave, Player: c.name,
		}), nil)

	case protocol.GroupInvite:
		if c.group == "" {
			c.send(protocol.FormatErr(protocol.ErrNotInGroup))
			return
		}
		c.send(protocol.FormatOK(""))
		// D12: no RFC error for "no such player" — fire-and-forget, like
		// inviting someone who isn't listening.
		s.hub.SendTo(cmd.Arg, protocol.FormatEvent(protocol.Event{
			Scope: protocol.EvtGroup, Kind: protocol.KindInvite, Player: c.name,
		}))
	}
}

// nonNil turns a nil slice into an empty one so it marshals as "[]", never
// "null" — the subject's own examples always show "[]".
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// broadcastStats pushes the current player count to every connected client.
func (s *Server) broadcastStats() {
	s.hub.Broadcast(protocol.FormatEvent(protocol.Event{
		Scope:   protocol.EvtStats,
		Players: s.hub.Count(),
	}))
}
