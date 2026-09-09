package server

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Enelsep/42_TAP/core/protocol"
	"github.com/Enelsep/42_TAP/core/world"
)

// Server owns the listening socket, the loaded world, and the hub of
// connected clients.
type Server struct {
	addr    string
	world   *world.World
	hub     *Hub
	reconns *reconnectTracker // abuse monitoring, D17
}

// New creates a Server that will listen on addr (e.g. ":4242") and place new
// players in w's start room.
func New(addr string, w *world.World) *Server {
	return &Server{addr: addr, world: w, hub: NewHub(w), reconns: newReconnectTracker()}
}

// Run listens on s.addr and blocks, accepting connections until the listener
// fails. Each connection is handled on its own goroutine.
func (s *Server) Run() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	slog.Info("listening", "addr", s.addr)

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
	remoteStr := remote.String()
	slog.Info("connected", "remote", remoteStr)
	if s.reconns.hit(remote, time.Now()) {
		slog.Warn("abuse", "type", "reconnect", "remote", remoteStr)
	}

	c := newClient(conn)
	go c.writeLoop()

	defer func() {
		// Closing conn is writeLoop's job (after it drains c.out) so a
		// reply queued right before disconnect is never lost to a race.
		if c.name != "" {
			// Remove state, *then* tell the room (and group, if any), *then*
			// the whole server — the order the subject requires for a clean
			// disconnect. room and group must be read before Unregister,
			// which clears the latter.
			room, group := c.room, c.group
			s.hub.Unregister(c)
			s.hub.BroadcastRoom(room, protocol.FormatEvent(protocol.Event{
				Scope:    protocol.EvtRoom,
				Kind:     protocol.KindPresence,
				Presence: protocol.PresenceLeave,
				Player:   c.name,
			}), nil)
			if group != "" {
				s.hub.BroadcastGroup(group, protocol.FormatEvent(protocol.Event{
					Scope: protocol.EvtGroup, Kind: protocol.KindLeave, Player: c.name,
				}), nil)
			}
			s.broadcastStats()
		} else {
			close(c.out)
		}
		slog.Info("disconnected", "remote", remoteStr, "player", c.name)
	}()

	c.send(protocol.Greeting + protocol.LineTerm)

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()

		if c.rate.hit(time.Now()) {
			slog.Warn("abuse", "type", "flood", "player", c.name, "remote", remoteStr)
		}

		cmd, err := protocol.ParseCommand(line)
		if err != nil {
			c.send(protocol.FormatErr(protocol.ErrBadRequest))
			continue
		}

		slog.Info("command", "remote", remoteStr, "player", c.name,
			"verb", cmd.Verb, "scope", cmd.Scope, "sub", cmd.Sub, "arg", cmd.Arg)

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

		case protocol.VerbTake:
			s.handleTake(c, cmd)

		case protocol.VerbDrop:
			s.handleDrop(c, cmd)

		case protocol.VerbInventory:
			s.handleInventory(c)

		case protocol.VerbTalk:
			s.handleTalk(c, cmd)

		case protocol.VerbQuest:
			s.handleQuest(c, cmd)

		case protocol.VerbQuests:
			s.handleQuests(c)

		case protocol.VerbAttack:
			s.handleAttack(c, cmd)

		case protocol.VerbStatus:
			s.handleStatus(c)

		case protocol.VerbDefend:
			s.handleDefend(c)

		case protocol.VerbFlee:
			s.handleFlee(c)

		default:
			// Unreachable in theory — ParseCommand only ever returns a Verb
			// with a case above — but a WARN here means a future Verb added
			// to protocol.go without a matching case fails loud, not silent.
			slog.Warn("unhandled verb", "remote", remoteStr, "player", c.name, "verb", cmd.Verb)
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
	slog.Info("player connected", "remote", c.conn.RemoteAddr().String(), "player", c.name, "room", c.room)
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
// on the floor — the floor comes from the hub's dynamic state, which is what
// TAKE/DROP actually mutate. The NPC list goes through RoomNPC rather than
// the world's static Spawns, so a killed enemy stops showing up (D15).
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
		Items:   nonNil(s.hub.RoomItems(c.room)),
		NPCs:    []string{},
	}
	if npc := s.hub.RoomNPC(c.room); npc != nil {
		reply.NPCs = []string{npc.ID}
	}

	data, err := json.Marshal(reply)
	if err != nil {
		slog.Error("marshal failed", "player", c.name, "reply", "look", "err", err)
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
	if item, gated := loc.Requires[dir]; gated && !s.hub.Holds(c, item) {
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

// handleTake moves an item from c's room to c's inventory, resolved by
// canonical id or case-insensitive display name (RFC §8.3/8.4).
func (s *Server) handleTake(c *Client, cmd protocol.Command) {
	id, ok := s.hub.TakeItem(c, c.room, cmd.Arg)
	if !ok {
		c.send(protocol.FormatErr(protocol.ErrItemNotFound))
		return
	}
	slog.Info("item taken", "player", c.name, "item", id, "room", c.room)
	c.send(protocol.FormatOK("taken=" + id))
}

// handleDrop moves an item from c's inventory to c's room, same resolution
// rule as TAKE.
func (s *Server) handleDrop(c *Client, cmd protocol.Command) {
	id, ok := s.hub.DropItem(c, c.room, cmd.Arg)
	if !ok {
		c.send(protocol.FormatErr(protocol.ErrItemNotInInv))
		return
	}
	slog.Info("item dropped", "player", c.name, "item", id, "room", c.room)
	c.send(protocol.FormatOK("dropped=" + id))
}

// handleInventory replies with the canonical ids c is carrying.
func (s *Server) handleInventory(c *Client) {
	data, err := json.Marshal(s.hub.Inventory(c))
	if err != nil {
		slog.Error("marshal failed", "player", c.name, "reply", "inventory", "err", err)
		return
	}
	c.send(protocol.FormatOK(string(data)))
}

// handleTalk replies with the NPC's next dialogue line — or, if this TALK
// just closed a deliver quest, the quest's own Complete line instead (D10:
// completion is a side effect of the RFC commands, never a separate reply).
func (s *Server) handleTalk(c *Client, cmd protocol.Command) {
	npc := s.hub.NPCIn(c.room, cmd.Arg)
	if npc == nil {
		c.send(protocol.FormatErr(protocol.ErrNPCNotFound))
		return
	}
	if line, quest, ok := s.hub.CompleteDelivery(c, npc); ok {
		slog.Info("quest completed", "player", c.name, "quest", quest, "npc", npc.ID)
		c.send(protocol.FormatOK(line))
		return
	}
	c.send(protocol.FormatOK(s.hub.TalkLine(npc)))
}

// handleQuest replies with the quest npc offers, accepting it on the spot if
// this is the first time c has asked (D10 — QUEST is both offer and accept).
func (s *Server) handleQuest(c *Client, cmd protocol.Command) {
	npc := s.hub.NPCIn(c.room, cmd.Arg)
	if npc == nil {
		c.send(protocol.FormatErr(protocol.ErrNPCNotFound))
		return
	}
	q, description, status, justAccepted, ok := s.hub.QuestInfo(c, npc)
	if !ok {
		c.send(protocol.FormatErr(protocol.ErrNoQuestAvailable))
		return
	}
	if justAccepted {
		slog.Info("quest accepted", "player", c.name, "quest", q.ID)
	}
	data, err := json.Marshal(protocol.QuestReply{
		QuestID:     q.ID,
		Description: description,
		Reward:      q.Reward,
		Status:      status,
	})
	if err != nil {
		slog.Error("marshal failed", "player", c.name, "reply", "quest", "err", err)
		return
	}
	c.send(protocol.FormatOK(string(data)))
}

// handleQuests replies with every quest c has taken, active or completed.
func (s *Server) handleQuests(c *Client) {
	data, err := json.Marshal(s.hub.QuestsFor(c))
	if err != nil {
		slog.Error("marshal failed", "player", c.name, "reply", "quests", "err", err)
		return
	}
	c.send(protocol.FormatOK(string(data)))
}

// handleAttack resolves one ATTACK turn (D15): c's hit, then npc's counter if
// it survives, both inside Hub.AttackNPC's single lock acquisition so two
// players finishing the same NPC off can never both trigger its death.
func (s *Server) handleAttack(c *Client, cmd protocol.Command) {
	npc := s.hub.NPCIn(c.room, cmd.Arg)
	if npc == nil {
		c.send(protocol.FormatErr(protocol.ErrNPCNotFound))
		return
	}
	if npc.Role != world.RoleEnemy {
		c.send(protocol.FormatErr(protocol.ErrNPCNotHostile))
		return
	}

	oldRoom := c.room
	reply, npcDied, respawned, ok := s.hub.AttackNPC(c, npc)
	if !ok {
		// Died to someone else between resolution and this call — to c the
		// effect is the same as it never having been here.
		c.send(protocol.FormatErr(protocol.ErrNPCNotFound))
		return
	}

	data, err := json.Marshal(reply)
	if err != nil {
		slog.Error("marshal failed", "player", c.name, "reply", "attack", "err", err)
		return
	}
	c.send(protocol.FormatOK(string(data)))

	if npcDied {
		slog.Info("npc killed", "player", c.name, "npc", npc.ID)
		// Room occupants otherwise only learn npc is gone on their next LOOK.
		s.hub.BroadcastRoom(oldRoom, protocol.FormatEvent(protocol.Event{
			Scope: protocol.EvtRoom, Kind: protocol.KindNPCDeath, NPC: npc.ID,
		}), c) // c already knows: its own AttackReply carries target_hp:0
	}
	if respawned {
		slog.Info("player respawned", "player", c.name, "cause", npc.ID, "room", s.world.Start)
		s.hub.BroadcastRoom(oldRoom, protocol.FormatEvent(protocol.Event{
			Scope: protocol.EvtRoom, Kind: protocol.KindPresence, Presence: protocol.PresenceLeave, Player: c.name,
		}), nil)
		s.hub.BroadcastRoom(s.world.Start, protocol.FormatEvent(protocol.Event{
			Scope: protocol.EvtRoom, Kind: protocol.KindPresence, Presence: protocol.PresenceEnter, Player: c.name,
		}), c)
	}
}

// handleStatus replies with c's current HP and derived status (D15).
func (s *Server) handleStatus(c *Client) {
	data, err := json.Marshal(s.hub.StatusOf(c))
	if err != nil {
		slog.Error("marshal failed", "player", c.name, "reply", "status", "err", err)
		return
	}
	c.send(protocol.FormatOK(string(data)))
}

// handleDefend arms DEFEND (D16): a bare OK, since the whole effect is
// internal and only shows up as a smaller number the next time c is hit.
func (s *Server) handleDefend(c *Client) {
	s.hub.Defend(c)
	c.send(protocol.FormatOK(""))
}

// handleFlee forces a random valid move, taking one free counter-attack from
// any live enemy in the room on the way out (D16). The reply carries that
// hit's damage and c's resulting status alongside the room move, so it
// isn't the one combat outcome the wire never reports.
func (s *Server) handleFlee(c *Client) {
	oldRoom := c.room
	reply, respawned, ok := s.hub.Flee(c)
	if !ok {
		c.send(protocol.FormatErr(protocol.ErrNoExit))
		return
	}

	data, err := json.Marshal(reply)
	if err != nil {
		slog.Error("marshal failed", "player", c.name, "reply", "flee", "err", err)
		return
	}
	c.send(protocol.FormatOK(string(data)))

	s.hub.BroadcastRoom(oldRoom, protocol.FormatEvent(protocol.Event{
		Scope: protocol.EvtRoom, Kind: protocol.KindPresence, Presence: protocol.PresenceLeave, Player: c.name,
	}), nil)
	s.hub.BroadcastRoom(reply.Room, protocol.FormatEvent(protocol.Event{
		Scope: protocol.EvtRoom, Kind: protocol.KindPresence, Presence: protocol.PresenceEnter, Player: c.name,
	}), c)
	if respawned {
		slog.Info("player respawned", "player", c.name, "cause", "flee", "room", reply.Room)
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
