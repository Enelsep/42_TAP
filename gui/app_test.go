package main

import (
	"net"
	"strings"
	"testing"

	"github.com/Enelsep/42_TAP/core/protocol"
	"github.com/Enelsep/42_TAP/core/server"
	"github.com/Enelsep/42_TAP/core/world"
)

// startServer runs a real TAP server on a free port and returns its address,
// so the backend is exercised over an actual socket rather than a stub.
func startServer(t *testing.T) string {
	t.Helper()
	w, err := world.Load("../data/world.json")
	if err != nil {
		t.Fatalf("load world: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // hand the port straight over to the server

	go server.New(addr, w).Run()
	for range 50 {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr
		}
	}
	t.Fatalf("server never came up on %s", addr)
	return ""
}

func TestSessionRoundTrip(t *testing.T) {
	addr := startServer(t)
	a := NewApp()

	if a.Connected() {
		t.Error("a fresh App must not report a connection")
	}
	if _, err := a.Look(); err != errNotConnected {
		t.Errorf("Look before Connect = %v, want errNotConnected", err)
	}
	if err := a.Connect(addr, "alice"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !a.Connected() {
		t.Error("Connected() is false after a successful Connect")
	}

	// Connecting emits EVT STATS. If the reader mistook that event for a
	// reply, this LOOK would receive it instead of its own payload — so a
	// clean decode here is what proves events and replies stay separate.
	look, err := a.Look()
	if err != nil {
		t.Fatalf("Look: %v", err)
	}
	if look.Room.ID != "loc.start" || look.Room.Name == "" {
		t.Errorf("Look = %+v", look.Room)
	}
	if len(look.Players) != 1 || look.Players[0] != "alice" {
		t.Errorf("players = %v, want [alice]", look.Players)
	}

	room, err := a.Move("north")
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if room != "loc.suburbs" {
		t.Errorf("Move north = %q, want loc.suburbs", room)
	}
	if look, _ = a.Look(); look.Room.ID != room {
		t.Errorf("after Move, Look reports %q", look.Room.ID)
	}

	if n, err := a.Who(); err != nil || n != 1 {
		t.Errorf("Who = %d, %v, want 1", n, err)
	}
	if data, err := a.Send("LOOK"); err != nil || !strings.Contains(data, "loc.suburbs") {
		t.Errorf("Send(LOOK) = %q, %v", data, err)
	}

	if err := a.Disconnect(); err != nil {
		t.Errorf("Disconnect: %v", err)
	}
}

// An ERR reply must surface as a Go error carrying code and symbol, which is
// what Wails hands the frontend as a rejected promise.
func TestErrorsReachTheCaller(t *testing.T) {
	addr := startServer(t)
	a := NewApp()
	if err := a.Connect(addr, "bob"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer a.Disconnect()

	_, err := a.Move("up")
	if err == nil || err.Error() != "301 NO_EXIT" {
		t.Errorf("Move up = %v, want 301 NO_EXIT", err)
	}

	// A second client claiming the same name is refused, and the failed
	// Connect must leave nothing half-open behind.
	other := NewApp()
	if err := other.Connect(addr, "bob"); err == nil || err.Error() != "201 NAME_IN_USE" {
		t.Errorf("duplicate Connect = %v, want 201 NAME_IN_USE", err)
	}
	if other.Connected() {
		t.Error("a refused Connect must not leave a live connection")
	}
}

func TestLocalValidation(t *testing.T) {
	a := NewApp()

	// Malformed input is rejected before it can reach the wire, so the GUI
	// only ever emits valid RFC commands.
	if _, err := a.Send("FLY north"); err != protocol.ErrBadRequest {
		t.Errorf("Send(FLY north) = %v, want a parse error", err)
	}
	if err := a.Chat("NOWHERE", "hi"); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Errorf("Chat with a bad scope = %v", err)
	}
	if err := a.Connect("127.0.0.1:1", "x"); err == nil {
		t.Error("Connect to a dead port must fail")
	}
}
