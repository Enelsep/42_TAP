package server

import (
	"net"
	"sync"
	"time"
)

const (
	floodWindow    = 2 * time.Second
	floodThreshold = 20 // commands within floodWindow

	reconnectWindow    = 10 * time.Second
	reconnectThreshold = 3 // connections from one address within reconnectWindow
)

type commandRate struct {
	recent []time.Time
}

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
