package server

import (
	"net"
	"sync"
	"time"
)

// Abuse-monitoring thresholds (RFC §9.4, SHOULD). Counting and logging only
// — see D17 in docs/decisions.md — nothing here ever rejects a connection
// or a command.
const (
	floodWindow    = 2 * time.Second
	floodThreshold = 20 // commands within floodWindow

	reconnectWindow    = 10 * time.Second
	reconnectThreshold = 3 // connections from one address within reconnectWindow
)

// commandRate tracks one connection's recent command timestamps. It belongs
// to a single Client and is only ever touched by that connection's own
// reader goroutine (server.go's read loop), so — unlike everything in
// hub.go — it needs no lock of its own.
type commandRate struct {
	recent []time.Time
}

// hit records now and reports whether this command just pushed the
// connection over floodThreshold within floodWindow. It reports true only on
// the tick that crosses the line, not on every one after, so a flood logs
// one WARN, not one per command for as long as it lasts.
func (r *commandRate) hit(now time.Time) bool {
	cutoff := now.Add(-floodWindow)
	kept := r.recent[:0]
	for _, t := range r.recent {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	r.recent = append(kept, now)
	return len(r.recent) == floodThreshold
}

// reconnectTracker flags an address reconnecting suspiciously fast, across
// separate TCP connections — state no single Client can hold, since a fresh
// one exists per socket. One Server owns one tracker, shared by every
// connection's goroutine, so hit is mutex-guarded.
type reconnectTracker struct {
	mu     sync.Mutex
	recent map[string][]time.Time
}

func newReconnectTracker() *reconnectTracker {
	return &reconnectTracker{recent: make(map[string][]time.Time)}
}

// hit records a connection from addr (as returned by net.Conn.RemoteAddr)
// and reports whether it just crossed reconnectThreshold within
// reconnectWindow — true only on the crossing tick, same reasoning as
// commandRate.hit.
func (t *reconnectTracker) hit(addr net.Addr, now time.Time) bool {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := now.Add(-reconnectWindow)
	kept := t.recent[host][:0]
	for _, at := range t.recent[host] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	t.recent[host] = append(kept, now)
	return len(t.recent[host]) == reconnectThreshold
}
