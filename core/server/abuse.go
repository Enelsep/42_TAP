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

// commandRate tracks one connection's recent command timestamps. Only ever
// touched by that connection's own reader goroutine, so it needs no lock.
type commandRate struct {
	recent []time.Time
}

// hit records now and reports whether the connection just crossed
// floodThreshold within floodWindow — true only on that one tick, so a
// flood logs one WARN, not one per command.
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
// separate connections — state no single Client can hold. Shared by every
// connection's goroutine, so hit is mutex-guarded.
type reconnectTracker struct {
	mu     sync.Mutex
	recent map[string][]time.Time
}

func newReconnectTracker() *reconnectTracker {
	return &reconnectTracker{recent: make(map[string][]time.Time)}
}

// hit records a connection from addr and reports whether it just crossed
// reconnectThreshold within reconnectWindow — same one-tick reasoning as
// commandRate.hit.
//
// Also sweeps every other host's stale entries on each call, deleting any
// that empty out — otherwise a host seen once keeps a slice forever, and
// the map grows unbounded over a long-running server's life.
func (t *reconnectTracker) hit(addr net.Addr, now time.Time) bool {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := now.Add(-reconnectWindow)
	for h, ats := range t.recent {
		kept := ats[:0]
		for _, at := range ats {
			if at.After(cutoff) {
				kept = append(kept, at)
			}
		}
		if len(kept) == 0 {
			delete(t.recent, h)
		} else {
			t.recent[h] = kept
		}
	}

	t.recent[host] = append(t.recent[host], now)
	return len(t.recent[host]) == reconnectThreshold
}
