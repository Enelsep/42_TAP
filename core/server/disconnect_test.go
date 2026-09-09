package server_test

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// T4.2's disconnect torture test, sharing the one server instance
// TestMain (gauntlet_test.go) starts for the whole package.

// kill aborts c's connection the way kill -9 does to a real client process
// — an immediate RST, no FIN handshake — rather than a plain Close, which
// the server's read loop sees as EOF and cannot tell apart from a
// deliberate QUIT. SetLinger(0) is what forces the RST.
func (c *client) kill() {
	c.t.Helper()
	if tc, ok := c.conn.(*net.TCPConn); ok {
		tc.SetLinger(0)
	}
	c.conn.Close()
}

// chatSpam fires n GLOBAL chat messages with no read in between — the
// point is broadcast pressure on the server, not this client's own view of
// the replies, and letting them pile up unread in the OS socket buffer is
// fine at this volume.
func (c *client) chatSpam(n int, tag string) {
	c.t.Helper()
	for i := 0; i < n; i++ {
		c.sendRaw(fmt.Sprintf("CHAT GLOBAL %s-%d\n", tag, i))
	}
}

// readUntil reads lines — EVT included, unlike reply() — until pred
// matches one or timeout elapses, discarding everything that doesn't
// match. Returns "" on timeout.
func (c *client) readUntil(timeout time.Duration, pred func(line string) bool) string {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ""
		}
		c.conn.SetReadDeadline(time.Now().Add(remaining))
		line, err := c.r.ReadString('\n')
		if err != nil {
			return ""
		}
		line = strings.TrimRight(line, "\r\n")
		if pred(line) {
			return line
		}
	}
}

// lastStats drains c's stream until idleTimeout passes with nothing new
// arriving (bounded overall by hardTimeout), returning the players value
// of the last EVT STATS line seen, or -1 if none arrived. Everything else
// in the stream — spam's own OK acks, other CHAT broadcasts — is
// discarded; this is purely "what did the player count settle on".
func (c *client) lastStats(idleTimeout, hardTimeout time.Duration) int {
	c.t.Helper()
	deadline := time.Now().Add(hardTimeout)
	last := -1
	for time.Now().Before(deadline) {
		c.conn.SetReadDeadline(time.Now().Add(idleTimeout))
		line, err := c.r.ReadString('\n')
		if err != nil {
			break // idle timeout: nothing more arriving
		}
		line = strings.TrimRight(line, "\r\n")
		if n, ok := strings.CutPrefix(line, "EVT STATS players="); ok {
			if v, err := strconv.Atoi(n); err == nil {
				last = v
			}
		}
	}
	return last
}

// TestDisconnectTorture connects 20 clients, has all of them chat-spam
// concurrently, and kills 10 of them mid-spam with an abrupt RST (the
// roadmap's "kill -9" stand-in — a real SIGKILL can't be delivered to a Go
// net.Conn in-process, and an RST is what the server actually sees from
// one). The 10 survivors must keep receiving broadcasts and WHO/EVT STATS
// must settle on the correct count: proof that a dead client sitting in
// h.clients mid-broadcast can't stall or corrupt delivery to everyone
// else — Client.send's non-blocking buffered channel (D17: "a slow or dead
// client can never block a broadcast to everyone else") under actual
// concurrent load, not just by inspection.
func TestDisconnectTorture(t *testing.T) {
	const total, killed = 20, 10
	clients := make([]*client, total)
	for i := range clients {
		clients[i] = connected(t, fmt.Sprintf("torture%d", i))
	}
	survivors := clients[killed:]

	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c *client) {
			defer wg.Done()
			c.chatSpam(5, "spam")
		}(c)
	}
	// Kill the first `killed` clients staggered across the spam window,
	// not before or after it, so the hub's broadcast loop genuinely has
	// to iterate over dying connections mid-flight rather than ones that
	// were already gone or still fully healthy.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, c := range clients[:killed] {
			time.Sleep(time.Millisecond)
			c.kill()
		}
	}()
	wg.Wait()

	// Each kill's Unregister (and the EVT STATS broadcast it triggers) runs
	// on that connection's own reader goroutine, asynchronously — drain
	// survivors[0]'s stream until it goes quiet to find the settled count,
	// rather than racing a fixed sleep against 10 independent goroutines.
	want := len(survivors)
	if got := survivors[0].lastStats(300*time.Millisecond, 5*time.Second); got != want {
		t.Fatalf("EVT STATS settled at players=%d, want %d", got, want)
	}

	// A direct WHO query on the same connection must agree — by now
	// survivors[0]'s stream is quiet (lastStats only returns once idle),
	// so this reply isn't racing anything new.
	survivors[0].send("WHO")
	if who, wantWho := survivors[0].reply(), "OK players="+strconv.Itoa(want); who != wantWho {
		t.Errorf("WHO = %q, want %q", who, wantWho)
	}

	// Every *other* survivor's stream is still untouched — each must also
	// have seen the settled count broadcast to it, independently of
	// survivors[0]'s view.
	for _, c := range survivors[1:] {
		if got := c.readUntil(3*time.Second, func(l string) bool {
			return l == "EVT STATS players="+strconv.Itoa(want)
		}); got == "" {
			t.Errorf("%v: never observed EVT STATS players=%d", c.conn.RemoteAddr(), want)
		}
	}

	// Broadcast delivery itself: one survivor speaks, every other survivor
	// must hear it. This is the actual "still receive events" assertion —
	// WHO/STATS only prove the hub's bookkeeping is correct, not that a
	// fresh broadcast still reaches everyone alive.
	const proof = "final-proof"
	survivors[0].send("CHAT GLOBAL " + proof)
	wantEvt := "EVT GLOBAL CHAT torture" + strconv.Itoa(killed) + " " + proof
	for _, c := range survivors[1:] {
		if got := c.readUntil(2*time.Second, func(l string) bool { return l == wantEvt }); got == "" {
			t.Errorf("%v: never received %q", c.conn.RemoteAddr(), wantEvt)
		}
	}
}
