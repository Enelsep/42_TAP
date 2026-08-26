// Resolutions of the RFC's ambiguities are marked "quirk" in the comments
// below and are mirrored in docs/decisions.md and the README.
package protocol

import (
	"strconv"
	"strings"
)

const (
	ProtoVersion = 1
	Greeting     = "OK hello proto=1"
	LineTerm     = "\n"
	MaxLineLen   = 1024
)

type Verb string

const (
	VerbConnect   Verb = "CONNECT"   // CONNECT <username>          → OK connected
	VerbLook      Verb = "LOOK"      // LOOK                        → OK <LookReply JSON>
	VerbMove      Verb = "MOVE"      // MOVE <direction>            → OK room=<room.id>
	VerbQuit      Verb = "QUIT"      // QUIT                        → OK bye
	VerbChat      Verb = "CHAT"      // CHAT <scope> <message…>     → OK
	VerbWho       Verb = "WHO"       // WHO                         → OK players=<count>
	VerbGroup     Verb = "GROUP"     // GROUP <subcommand> [arg]    → see GroupSub
	VerbTake      Verb = "TAKE"      // TAKE <item…>                → OK taken=<item.id>
	VerbDrop      Verb = "DROP"      // DROP <item…>                → OK dropped=<item.id>
	VerbInventory Verb = "INVENTORY" // INVENTORY                   → OK <InventoryReply JSON>
	VerbTalk      Verb = "TALK"      // TALK <npc>                  → OK <dialogue text>
	VerbAttack    Verb = "ATTACK"    // ATTACK <npc>                → OK <AttackReply JSON>
	VerbStatus    Verb = "STATUS"    // STATUS                      → OK <StatusReply JSON>
	VerbQuest     Verb = "QUEST"     // QUEST <npc>                 → OK <QuestReply JSON>
	VerbQuests    Verb = "QUESTS"    // QUESTS                      → OK <QuestsReply JSON>
)

type ChatScope string

const (
	ChatGlobal ChatScope = "GLOBAL"
	ChatRoom   ChatScope = "ROOM"
	ChatGroup  ChatScope = "GROUP"
)

type GroupSub string

const (
	GroupCreate GroupSub = "CREATE"
	GroupInvite GroupSub = "INVITE"
	GroupJoin   GroupSub = "JOIN"
	GroupLeave  GroupSub = "LEAVE"
)

type Command struct {
	Verb  Verb
	Scope ChatScope // CHAT only
	Sub   GroupSub  // GROUP only
	Arg   string
}

type Reply struct {
	Err  *Error `json:"err,omitempty"`  // nil on success
	Data string `json:"data,omitempty"` // everything after "OK", or after the error symbol
}

func (r Reply) OK() bool { return r.Err == nil }

// Quirk #3: code 404 covers three distinct conditions (ITEM_NOT_FOUND,
// ITEM_NOT_IN_INVENTORY, NPC_NOT_FOUND), so callers MUST key on Symbol and
// never on Code alone. That is why both halves are kept.
type Error struct {
	Code   int    `json:"code"`
	Symbol string `json:"symbol"`
}

// Error renders the error as it appears on the wire, without the "ERR " prefix.
func (e *Error) Error() string { return strconv.Itoa(e.Code) + " " + e.Symbol }

// Fatal reports whether this is a 9xx system error, after which a client
// should re-establish the connection. 4xx errors are recoverable (§7.3).
func (e *Error) Fatal() bool { return e.Code >= 900 }

// Error codes (§8.2). Note that 404 is shared by three conditions.
const (
	CodeNameInUse        = 201
	CodeNoExit           = 301
	CodeNotInGroup       = 401
	CodeAlreadyInGroup   = 402
	CodeNotFound         = 404
	CodeNPCNotHostile    = 405
	CodeNoQuestAvailable = 406
	CodeConnectionFailed = 900
	CodeSendFailed       = 901
)

// Error symbols (§8.2). These are the values clients dispatch on.
const (
	SymNameInUse        = "NAME_IN_USE"
	SymNoExit           = "NO_EXIT"
	SymNotInGroup       = "NOT_IN_GROUP"
	SymAlreadyInGroup   = "ALREADY_IN_GROUP"
	SymItemNotFound     = "ITEM_NOT_FOUND"
	SymItemNotInInv     = "ITEM_NOT_IN_INVENTORY"
	SymNPCNotFound      = "NPC_NOT_FOUND"
	SymNPCNotHostile    = "NPC_NOT_HOSTILE"
	SymNoQuestAvailable = "NO_QUEST_AVAILABLE"
	SymConnectionFailed = "CONNECTION_FAILED"
	SymSendFailed       = "SEND_FAILED"
)

// The complete error table of §8.2, ready to be returned by handlers.
// The 9xx pair is mostly client-side diagnostics.
var (
	ErrNameInUse        = &Error{CodeNameInUse, SymNameInUse}
	ErrNoExit           = &Error{CodeNoExit, SymNoExit}
	ErrNotInGroup       = &Error{CodeNotInGroup, SymNotInGroup}
	ErrAlreadyInGroup   = &Error{CodeAlreadyInGroup, SymAlreadyInGroup}
	ErrItemNotFound     = &Error{CodeNotFound, SymItemNotFound}
	ErrItemNotInInv     = &Error{CodeNotFound, SymItemNotInInv}
	ErrNPCNotFound      = &Error{CodeNotFound, SymNPCNotFound}
	ErrNPCNotHostile    = &Error{CodeNPCNotHostile, SymNPCNotHostile}
	ErrNoQuestAvailable = &Error{CodeNoQuestAvailable, SymNoQuestAvailable}
	ErrConnectionFailed = &Error{CodeConnectionFailed, SymConnectionFailed}
	ErrSendFailed       = &Error{CodeSendFailed, SymSendFailed}
)

// Non-RFC codes. §8.2 defines none for a command sent before CONNECT, nor for
// a malformed line, yet §3.3 and §9.3 require both to be handled. Ours, kept in
// RFC-consistent ranges (2xx session, 4xx recoverable) — see docs/decisions.md.
const (
	CodeNotConnected = 202
	CodeBadRequest   = 400

	SymNotConnected = "NOT_CONNECTED"
	SymBadRequest   = "BAD_REQUEST"
)

var (
	// gameplay command received in state CONNECTED, before a successful CONNECT
	ErrNotConnected = &Error{CodeNotConnected, SymNotConnected}
	// unparsable line: unknown verb, missing or invalid argument, over MaxLineLen
	ErrBadRequest = &Error{CodeBadRequest, SymBadRequest}
)

// EventPrefix starts every event line: "EVT <event-type> <event-data>" (§7.1).
const EventPrefix = "EVT"

// EventScope is the first token after EVT: the audience the event concerns.
type EventScope string

const (
	EvtRoom   EventScope = "ROOM"   // concerns the player's current room
	EvtGlobal EventScope = "GLOBAL" // concerns the whole server
	EvtGroup  EventScope = "GROUP"  // concerns the player's group
	EvtStats  EventScope = "STATS"  // server-wide counters
)

// EventKind is the second token, absent for EvtStats.
type EventKind string

const (
	KindPresence EventKind = "PRESENCE" // ROOM only, carries a Presence
	KindChat     EventKind = "CHAT"     // ROOM, GLOBAL and GROUP
	KindInvite   EventKind = "INVITE"   // GROUP only
	KindJoin     EventKind = "JOIN"     // GROUP only
	KindLeave    EventKind = "LEAVE"    // GROUP only
)

// Presence is the third token of a room presence event.
type Presence string

const (
	PresenceEnter Presence = "ENTER"
	PresenceLeave Presence = "LEAVE"
)

// Quirk #4: GROUP INVITE carries only a player name, so the invitee cannot
// tell which group to JOIN. Our clients therefore keep Raw around and tolerate
// trailing tokens they do not understand, rather than rejecting the line —
// other groups may resolve this differently.
type Event struct {
	Scope    EventScope `json:"scope"`
	Kind     EventKind  `json:"kind,omitempty"`     // empty for EvtStats
	Presence Presence   `json:"presence,omitempty"` // KindPresence only
	Player   string     `json:"player,omitempty"`   // subject of the event
	Message  string     `json:"message,omitempty"`  // KindChat only
	Players  int        `json:"players,omitempty"`  // EvtStats only

	// Raw is the event line as received, terminator stripped. It lets clients
	// log or display events whose type they do not recognise instead of
	// dropping the connection (roadmap T6.4).
	Raw string `json:"raw,omitempty"`
}

// --- parsing and formatting ---
//
// Every Format* result already ends with LineTerm: framing belongs to the
// protocol, not to its callers, so an unterminated line can never reach the
// wire. Every Parse* accepts a line with or without its terminator.
//
// Tolerance rule (D1, D4): unknown constructs are accepted — extra tokens,
// unknown event scopes and kinds — but a malformed *known* construct is an
// error. Only ErrBadRequest is ever returned; the server turns it into a wire
// response, clients merely log it.

// trimLine strips the terminator and surrounding whitespace, including the
// trailing CR of D1.
func trimLine(s string) string { return strings.Trim(s, " \t\r\n") }

// cut splits off the first space-separated token, tolerating repeated spaces.
func cut(s string) (head, rest string) {
	head, rest, _ = strings.Cut(s, " ")
	return head, strings.TrimLeft(rest, " ")
}

// ParseCommand parses a client → server line. Verbs and keywords are
// case-insensitive (§4.2); Arg is the untouched rest of the line (D6).
func ParseCommand(line string) (Command, error) {
	if len(line) > MaxLineLen {
		return Command{}, ErrBadRequest
	}
	verb, rest := cut(trimLine(line))
	c := Command{Verb: Verb(strings.ToUpper(verb)), Arg: rest}

	switch c.Verb {
	case VerbLook, VerbQuit, VerbWho, VerbInventory, VerbStatus, VerbQuests:
		c.Arg = "" // takes no argument; trailing tokens are ignored

	case VerbConnect:
		// A username is echoed inside events, where a space would make the
		// line ambiguous — so it must be a single token.
		if c.Arg == "" || strings.ContainsAny(c.Arg, " \t") {
			return Command{}, ErrBadRequest
		}

	case VerbMove, VerbTake, VerbDrop, VerbTalk, VerbAttack, VerbQuest:
		if c.Arg == "" {
			return Command{}, ErrBadRequest
		}

	case VerbChat:
		scope, msg := cut(rest)
		c.Scope, c.Arg = ChatScope(strings.ToUpper(scope)), msg
		switch c.Scope {
		case ChatGlobal, ChatRoom, ChatGroup:
		default:
			return Command{}, ErrBadRequest
		}
		if msg == "" {
			return Command{}, ErrBadRequest
		}

	case VerbGroup:
		sub, arg := cut(rest)
		c.Sub, c.Arg = GroupSub(strings.ToUpper(sub)), arg
		switch c.Sub {
		case GroupCreate, GroupLeave:
			c.Arg = ""
		case GroupInvite, GroupJoin:
			if c.Arg == "" {
				return Command{}, ErrBadRequest
			}
		default:
			return Command{}, ErrBadRequest
		}

	default:
		return Command{}, ErrBadRequest
	}
	return c, nil
}

// FormatCommand renders a command as a wire line.
func FormatCommand(c Command) string {
	var b strings.Builder
	b.WriteString(string(c.Verb))
	switch c.Verb {
	case VerbChat:
		b.WriteString(" " + string(c.Scope))
	case VerbGroup:
		b.WriteString(" " + string(c.Sub))
	}
	if c.Arg != "" {
		b.WriteString(" " + c.Arg)
	}
	b.WriteString(LineTerm)
	return b.String()
}

// FormatOK renders a success response; data may be empty.
func FormatOK(data string) string {
	if data == "" {
		return "OK" + LineTerm
	}
	return "OK " + data + LineTerm
}

// FormatErr renders an error response: "ERR <code> <symbol>" (§8.1).
func FormatErr(e *Error) string { return "ERR " + e.Error() + LineTerm }

// ParseReply parses a server → client response line.
func ParseReply(line string) (Reply, error) {
	head, rest := cut(trimLine(line))
	switch strings.ToUpper(head) {
	case "OK":
		return Reply{Data: rest}, nil
	case "ERR":
		codeTok, tail := cut(rest)
		code, err := strconv.Atoi(codeTok)
		if err != nil {
			return Reply{}, ErrBadRequest
		}
		symbol, data := cut(tail)
		return Reply{Err: &Error{code, strings.ToUpper(symbol)}, Data: data}, nil
	}
	return Reply{}, ErrBadRequest
}

// IsEvent reports whether a server line is an event rather than a response,
// so clients can route it without parsing it twice.
func IsEvent(line string) bool {
	head, _ := cut(trimLine(line))
	return strings.EqualFold(head, EventPrefix)
}

// ParseEvent parses a server → client event line. An unrecognised scope or
// kind is not an error: the Event is returned with Raw set so the client can
// ignore or log it (D4).
func ParseEvent(line string) (Event, error) {
	raw := trimLine(line)
	head, rest := cut(raw)
	if !strings.EqualFold(head, EventPrefix) {
		return Event{}, ErrBadRequest
	}
	scope, rest := cut(rest)
	e := Event{Scope: EventScope(strings.ToUpper(scope)), Raw: raw}

	if e.Scope == EvtStats {
		field, _ := cut(rest)
		key, val, ok := strings.Cut(field, "=")
		if !ok || !strings.EqualFold(key, "players") {
			return Event{}, ErrBadRequest
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			return Event{}, ErrBadRequest
		}
		e.Players = n
		return e, nil
	}

	kind, rest := cut(rest)
	e.Kind = EventKind(strings.ToUpper(kind))
	switch e.Kind {
	case KindPresence:
		action, tail := cut(rest)
		e.Presence = Presence(strings.ToUpper(action))
		e.Player, _ = cut(tail)
	case KindChat:
		e.Player, e.Message = cut(rest)
	case KindInvite, KindJoin, KindLeave:
		e.Player, _ = cut(rest) // trailing tokens ignored (D4)
	}
	return e, nil
}

// FormatEvent renders an event as a wire line (§7.2).
func FormatEvent(e Event) string {
	if e.Scope == EvtStats {
		return EventPrefix + " " + string(EvtStats) + " players=" + strconv.Itoa(e.Players) + LineTerm
	}
	parts := []string{EventPrefix, string(e.Scope), string(e.Kind)}
	if e.Kind == KindPresence {
		parts = append(parts, string(e.Presence))
	}
	if e.Player != "" {
		parts = append(parts, e.Player)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, " ") + LineTerm
}
