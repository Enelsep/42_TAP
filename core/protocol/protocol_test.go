package protocol

import (
	"strings"
	"testing"
)

// Smoke tests for the parser and formatter. T1.2 expands this into the full
// table-driven suite covering every row of the command and event tables.

func TestCommandRoundTrip(t *testing.T) {
	cases := []struct {
		line string
		want Command
	}{
		{"CONNECT alice", Command{Verb: VerbConnect, Arg: "alice"}},
		{"LOOK", Command{Verb: VerbLook}},
		{"MOVE north", Command{Verb: VerbMove, Arg: "north"}},
		{"QUIT", Command{Verb: VerbQuit}},
		{"CHAT ROOM hello world", Command{Verb: VerbChat, Scope: ChatRoom, Arg: "hello world"}},
		{"CHAT GLOBAL hi", Command{Verb: VerbChat, Scope: ChatGlobal, Arg: "hi"}},
		{"WHO", Command{Verb: VerbWho}},
		{"GROUP CREATE", Command{Verb: VerbGroup, Sub: GroupCreate}},
		{"GROUP INVITE bob", Command{Verb: VerbGroup, Sub: GroupInvite, Arg: "bob"}},
		{"GROUP JOIN grp.1", Command{Verb: VerbGroup, Sub: GroupJoin, Arg: "grp.1"}},
		{"GROUP LEAVE", Command{Verb: VerbGroup, Sub: GroupLeave}},
		{"TAKE Rusty Sword", Command{Verb: VerbTake, Arg: "Rusty Sword"}},
		{"DROP item.herbs", Command{Verb: VerbDrop, Arg: "item.herbs"}},
		{"INVENTORY", Command{Verb: VerbInventory}},
		{"TALK npc.baker", Command{Verb: VerbTalk, Arg: "npc.baker"}},
		{"ATTACK goblin", Command{Verb: VerbAttack, Arg: "goblin"}},
		{"STATUS", Command{Verb: VerbStatus}},
		{"QUEST merchant", Command{Verb: VerbQuest, Arg: "merchant"}},
		{"QUESTS", Command{Verb: VerbQuests}},
	}
	for _, tc := range cases {
		got, err := ParseCommand(tc.line)
		if err != nil {
			t.Errorf("ParseCommand(%q): %v", tc.line, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseCommand(%q) = %+v, want %+v", tc.line, got, tc.want)
		}
		if formatted := FormatCommand(got); formatted != tc.line+LineTerm {
			t.Errorf("FormatCommand(%+v) = %q, want %q", got, formatted, tc.line+LineTerm)
		}
	}
}

// §4.2: command names are case-insensitive, and D1 tolerates a trailing CR.
func TestCommandTolerance(t *testing.T) {
	cases := []string{"move north", "Move north", "MOVE north\r\n", "  MOVE   north  "}
	want := Command{Verb: VerbMove, Arg: "north"}
	for _, line := range cases {
		got, err := ParseCommand(line)
		if err != nil {
			t.Errorf("ParseCommand(%q): %v", line, err)
			continue
		}
		if got != want {
			t.Errorf("ParseCommand(%q) = %+v, want %+v", line, got, want)
		}
	}
}

func TestCommandMalformed(t *testing.T) {
	cases := []string{
		"",                    // empty line
		"FLY north",           // unknown verb
		"MOVE",                // missing argument
		"CONNECT",             // missing username
		"CONNECT alice smith", // username must be one token
		"CHAT NOWHERE hi",     // unknown scope
		"CHAT ROOM",           // missing message
		"GROUP",               // missing subcommand
		"GROUP DISBAND",       // unknown subcommand
		"GROUP INVITE",        // missing player
		"TAKE " + strings.Repeat("x", MaxLineLen), // over MaxLineLen
	}
	for _, line := range cases {
		if _, err := ParseCommand(line); err != ErrBadRequest {
			t.Errorf("ParseCommand(%q) = %v, want ErrBadRequest", line, err)
		}
	}
}

func TestReply(t *testing.T) {
	if got, _ := ParseReply(Greeting); got.Data != "hello proto=1" || !got.OK() {
		t.Errorf("ParseReply(greeting) = %+v", got)
	}
	if got, _ := ParseReply("OK"); !got.OK() || got.Data != "" {
		t.Errorf(`ParseReply("OK") = %+v`, got)
	}
	// Keyed reply data is handed over verbatim; interpreting "taken=" is the
	// caller's job, not the parser's.
	if got, _ := ParseReply("OK taken=item.herbs"); got.Data != "taken=item.herbs" {
		t.Errorf("ParseReply(taken) = %+v", got)
	}

	got, err := ParseReply("ERR 404 ITEM_NOT_IN_INVENTORY")
	if err != nil {
		t.Fatalf("ParseReply(err line): %v", err)
	}
	if got.OK() || got.Err.Code != CodeNotFound || got.Err.Symbol != SymItemNotInInv {
		t.Errorf("ParseReply(err line) = %+v", got)
	}
	if got.Err.Fatal() {
		t.Error("404 must not be fatal")
	}
	if !ErrSendFailed.Fatal() {
		t.Error("901 must be fatal")
	}
	if line := FormatErr(ErrItemNotInInv); line != "ERR 404 ITEM_NOT_IN_INVENTORY"+LineTerm {
		t.Errorf("FormatErr = %q", line)
	}
	if _, err := ParseReply("MAYBE something"); err != ErrBadRequest {
		t.Error("non-response line must be rejected")
	}
}

func TestEventRoundTrip(t *testing.T) {
	cases := []struct {
		line string
		want Event
	}{
		{"EVT ROOM PRESENCE ENTER alice", Event{Scope: EvtRoom, Kind: KindPresence, Presence: PresenceEnter, Player: "alice"}},
		{"EVT ROOM PRESENCE LEAVE alice", Event{Scope: EvtRoom, Kind: KindPresence, Presence: PresenceLeave, Player: "alice"}},
		{"EVT ROOM CHAT alice hello world", Event{Scope: EvtRoom, Kind: KindChat, Player: "alice", Message: "hello world"}},
		{"EVT GLOBAL CHAT alice hi", Event{Scope: EvtGlobal, Kind: KindChat, Player: "alice", Message: "hi"}},
		{"EVT GROUP INVITE alice", Event{Scope: EvtGroup, Kind: KindInvite, Player: "alice"}},
		{"EVT GROUP JOIN bob", Event{Scope: EvtGroup, Kind: KindJoin, Player: "bob"}},
		{"EVT GROUP LEAVE bob", Event{Scope: EvtGroup, Kind: KindLeave, Player: "bob"}},
		{"EVT GROUP CHAT bob hey", Event{Scope: EvtGroup, Kind: KindChat, Player: "bob", Message: "hey"}},
		{"EVT STATS players=5", Event{Scope: EvtStats, Players: 5}},
	}
	for _, tc := range cases {
		got, err := ParseEvent(tc.line)
		if err != nil {
			t.Errorf("ParseEvent(%q): %v", tc.line, err)
			continue
		}
		if got.Raw != tc.line {
			t.Errorf("ParseEvent(%q).Raw = %q", tc.line, got.Raw)
		}
		got.Raw = ""
		if got != tc.want {
			t.Errorf("ParseEvent(%q) = %+v, want %+v", tc.line, got, tc.want)
		}
		if formatted := FormatEvent(got); formatted != tc.line+LineTerm {
			t.Errorf("FormatEvent(%+v) = %q, want %q", got, formatted, tc.line+LineTerm)
		}
		if !IsEvent(tc.line) {
			t.Errorf("IsEvent(%q) = false", tc.line)
		}
	}
}

// D4/T6.4: another group's extensions must not break our clients.
func TestEventTolerance(t *testing.T) {
	got, err := ParseEvent("EVT GROUP INVITE alice grp.7")
	if err != nil {
		t.Fatalf("trailing token rejected: %v", err)
	}
	if got.Player != "alice" {
		t.Errorf("Player = %q, want alice", got.Player)
	}

	if _, err := ParseEvent("EVT WEATHER RAIN heavy"); err != nil {
		t.Errorf("unknown event scope must be tolerated, got %v", err)
	}
	if _, err := ParseEvent("EVT ROOM PRESENCE ENTER alice\r"); err != nil {
		t.Errorf("trailing CR must be tolerated, got %v", err)
	}

	// A malformed *known* construct is still an error.
	if _, err := ParseEvent("EVT STATS players=many"); err != ErrBadRequest {
		t.Error("unparsable STATS count must be rejected")
	}
	if IsEvent("OK connected") {
		t.Error("IsEvent must not match a response")
	}
}

// Framing: nothing we emit may contain an embedded newline (D7).
func TestNoEmbeddedNewline(t *testing.T) {
	lines := []string{
		FormatCommand(Command{Verb: VerbChat, Scope: ChatRoom, Arg: "hello"}),
		FormatOK("room=loc.square"),
		FormatOK(""),
		FormatErr(ErrNoExit),
		FormatEvent(Event{Scope: EvtStats, Players: 3}),
	}
	for _, line := range lines {
		if !strings.HasSuffix(line, LineTerm) {
			t.Errorf("%q does not end with the terminator", line)
		}
		if strings.Contains(strings.TrimSuffix(line, LineTerm), "\n") {
			t.Errorf("%q contains an embedded newline", line)
		}
	}
}
