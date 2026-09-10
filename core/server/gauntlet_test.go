package server_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Enelsep/42_TAP/core/protocol"
	"github.com/Enelsep/42_TAP/core/server"
	"github.com/Enelsep/42_TAP/core/world"
)

// T4.1's malformed-input gauntlet, run against one real server instance for
// the whole file (TestMain below) rather than the package's protocol/world
// unit tests — this is deliberately a black-box test: every assertion goes
// through the same wire a real client would use, since that is exactly what
// the roadmap asks the gauntlet to exercise.

// testAddr is fixed rather than ":0" because Server.Run binds its own
// listener internally and never hands the chosen address back — acceptable
// for one server shared serially by this file's tests.
const testAddr = "127.0.0.1:42419"

func TestMain(m *testing.M) {
	w, err := world.Load("../../data/world.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "world.Load:", err)
		os.Exit(1)
	}
	if err := w.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "world invalid:", err)
		os.Exit(1)
	}
	go server.New(testAddr, w).Run() // outlives every test; errors are moot once dial-up succeeds below

	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.Dial("tcp", testAddr)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "server never came up on", testAddr)
			os.Exit(1)
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(m.Run())
}

// wantErr renders e the way it appears on the wire, terminator stripped, so
// assertions stay tied to the real error table instead of a hand-typed
// string that could silently drift from it.
func wantErr(e *protocol.Error) string {
	return strings.TrimSuffix(protocol.FormatErr(e), protocol.LineTerm)
}

// client wraps one raw TCP connection to the shared test server.
type client struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

// dial opens a fresh connection and consumes the greeting.
func dial(t *testing.T) *client {
	t.Helper()
	conn, err := net.Dial("tcp", testAddr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &client{t: t, conn: conn, r: bufio.NewReader(conn)}
	if got := c.reply(); got != protocol.Greeting {
		t.Fatalf("greeting = %q, want %q", got, protocol.Greeting)
	}
	return c
}

// connected dials and CONNECTs under name, failing the test on anything but
// success — the common case for gauntlet entries that need an authenticated
// session before the interesting part.
func connected(t *testing.T, name string) *client {
	t.Helper()
	c := dial(t)
	c.send("CONNECT " + name)
	if got, want := c.reply(), protocol.FormatOK("connected"); got != strings.TrimSuffix(want, protocol.LineTerm) {
		t.Fatalf("CONNECT %s = %q, want %q", name, got, want)
	}
	return c
}

func (c *client) send(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.conn, line+protocol.LineTerm); err != nil {
		c.t.Fatalf("write %q: %v", line, err)
	}
}

// sendRaw writes data verbatim, no terminator appended — for the
// fragmentation and coalescing tests, which control framing by hand.
func (c *client) sendRaw(data string) {
	c.t.Helper()
	if _, err := io.WriteString(c.conn, data); err != nil {
		c.t.Fatalf("write raw: %v", err)
	}
}

// reply reads one line, transparently skipping any EVT lines first — a
// real client's own command reply can be preceded by async broadcasts
// (EVT STATS on every CONNECT/disconnect, PRESENCE, ...) it did not cause,
// and a test that didn't skip them would flake on that unrelated traffic.
func (c *client) reply() string {
	c.t.Helper()
	for {
		c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, err := c.r.ReadString('\n')
		if err != nil {
			c.t.Fatalf("read: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, protocol.EventPrefix) {
			continue
		}
		return line
	}
}

// closed reports whether the server has closed the connection, within a
// short window — used to assert a genuine disconnect (the oversized-line
// case) rather than just checking the reply that preceded it.
func (c *client) closed() bool {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, err := c.r.ReadByte()
	return err != nil
}

func TestUnknownVerb(t *testing.T) {
	c := dial(t)
	c.send("FOOBAR")
	if got, want := c.reply(), wantErr(protocol.ErrBadRequest); got != want {
		t.Errorf("unknown verb = %q, want %q", got, want)
	}
}

func TestMissingArgument(t *testing.T) {
	c := connected(t, "missingarg")
	for _, cmd := range []string{"MOVE", "TAKE", "TALK", "ATTACK"} {
		c.send(cmd)
		if got, want := c.reply(), wantErr(protocol.ErrBadRequest); got != want {
			t.Errorf("%s (no arg) = %q, want %q", cmd, got, want)
		}
	}
}

func TestBinaryJunkVerb(t *testing.T) {
	c := dial(t)
	c.sendRaw("\x00\x01\x02 blah\n")
	if got, want := c.reply(), wantErr(protocol.ErrBadRequest); got != want {
		t.Errorf("binary junk verb = %q, want %q", got, want)
	}
}

func TestCommandBeforeConnect(t *testing.T) {
	c := dial(t)
	c.send("LOOK")
	if got, want := c.reply(), wantErr(protocol.ErrNotConnected); got != want {
		t.Errorf("LOOK before CONNECT = %q, want %q", got, want)
	}
}

// TestConnectRejectsControlChar covers T4.1's "control characters" item for
// client input: a username is echoed raw (not JSON) in every PRESENCE,
// GROUP and CHAT event for the rest of the session, so a control character
// in it — a terminal escape sequence, concretely — would ride along into
// every other player's raw T5.1 CLI. CONNECT rejects it at the door.
func TestConnectRejectsControlChar(t *testing.T) {
	c := dial(t)
	c.sendRaw("CONNECT ali\x07ce\n")
	if got, want := c.reply(), wantErr(protocol.ErrBadRequest); got != want {
		t.Errorf("CONNECT with control char in username = %q, want %q", got, want)
	}
}

// TestDoubleConnect asserts a second CONNECT on an already-authenticated
// connection is rejected without disturbing the first identity — no RFC
// code covers this state, so 201 NAME_IN_USE is reused (documented in
// docs/decisions.md), and this test is what proves reusing it doesn't
// silently corrupt c.name/c.room along the way.
func TestDoubleConnect(t *testing.T) {
	c := connected(t, "dblconnect")
	c.send("CONNECT someoneelse")
	if got, want := c.reply(), wantErr(protocol.ErrNameInUse); got != want {
		t.Errorf("second CONNECT = %q, want %q", got, want)
	}
	c.send("LOOK")
	if look := c.reply(); !strings.Contains(look, `"dblconnect"`) {
		t.Errorf("LOOK after rejected re-CONNECT = %q, want it to still list dblconnect", look)
	}
}

func TestTakeGhostItem(t *testing.T) {
	c := connected(t, "ghostitem")
	c.send("TAKE nonexistent_item_xyz")
	if got, want := c.reply(), wantErr(protocol.ErrItemNotFound); got != want {
		t.Errorf("TAKE ghost item = %q, want %q", got, want)
	}
}

func TestGroupJoinNonexistent(t *testing.T) {
	c := connected(t, "groupjoin")
	c.send("GROUP JOIN nosuchgroup")
	if got, want := c.reply(), wantErr(protocol.ErrGroupNotFound); got != want {
		t.Errorf("GROUP JOIN nonexistent = %q, want %q", got, want)
	}
}

// TestOverlongLine covers a line past MaxLineLen but still within the
// server's scan buffer — ParseCommand's own length check must be the one
// that rejects it, and the connection must stay usable afterward.
func TestOverlongLine(t *testing.T) {
	c := connected(t, "overlong")
	c.send("MOVE " + strings.Repeat("x", protocol.MaxLineLen)) // over MaxLineLen, comfortably under the server's scan buffer
	if got, want := c.reply(), wantErr(protocol.ErrBadRequest); got != want {
		t.Errorf("overlong line = %q, want %q", got, want)
	}
	c.send("LOOK")
	if got := c.reply(); !strings.HasPrefix(got, "OK ") {
		t.Errorf("LOOK after overlong line = %q, connection should still be usable", got)
	}
}

// TestHugeLineGetsAReply is the regression test for the gauntlet's one real
// finding: a line beyond bufio.Scanner's own buffer used to make Scan
// return false with no way to tell that apart from a clean disconnect, so
// the client got silently dropped instead of an ERR 400 (violating RFC
// §9.3's "SHOULD result in appropriate error responses"). The fix widens
// the scan buffer past MaxLineLen (so ParseCommand's own check is what
// fires for anything a real client could plausibly send) and replies once
// before the now-unrecoverable connection closes.
func TestHugeLineGetsAReply(t *testing.T) {
	c := connected(t, "hugeline")
	c.send("MOVE " + strings.Repeat("x", 8*protocol.MaxLineLen))
	if got, want := c.reply(), wantErr(protocol.ErrBadRequest); got != want {
		t.Errorf("huge line = %q, want %q", got, want)
	}
	if !c.closed() {
		t.Errorf("connection should be closed after a line past the scan buffer")
	}
}

// TestFragmentation covers RFC §9.2: a command split across two TCP writes,
// with a delay standing in for the two packets arriving separately, must
// still be assembled into one command.
func TestFragmentation(t *testing.T) {
	c := connected(t, "frag")
	c.sendRaw("MO")
	time.Sleep(50 * time.Millisecond)
	c.sendRaw("VE north\n")
	if got, want := c.reply(), "OK room=loc.suburbs"; got != want {
		t.Errorf("fragmented MOVE = %q, want %q", got, want)
	}
}

// TestCoalescing covers §9.2's other half: several commands arriving in one
// write/packet must still produce one reply per command, in order.
func TestCoalescing(t *testing.T) {
	c := connected(t, "coalesce")
	c.sendRaw("STATUS\nINVENTORY\nQUESTS\n")
	want := []string{
		`OK {"hp":100,"max_hp":100,"status":"healthy"}`,
		"OK []",
		"OK []",
	}
	for i, w := range want {
		if got := c.reply(); got != w {
			t.Errorf("coalesced reply %d = %q, want %q", i, got, w)
		}
	}
}

// TestNoLeakedPlayers runs last (declaration order) and checks every
// player from every test above — CONNECTed and either QUIT or simply
// closed via t.Cleanup — is gone from the hub. A leaked entry would mean a
// disconnect path left a ghost behind, exactly what T4.2 stress-tests
// further; this is the gauntlet's own cheap version of that assertion.
func TestNoLeakedPlayers(t *testing.T) {
	observer := dial(t)
	observer.send("CONNECT observer")
	observer.reply() // OK connected

	deadline := time.Now().Add(2 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		observer.send("WHO")
		last = observer.reply()
		if last == "OK players=1" { // just the observer itself
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("WHO settled at %q, want \"OK players=1\" (observer only) — a prior test leaked a player", last)
}
