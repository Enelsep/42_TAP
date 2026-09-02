package server

import (
	"bufio"
	"log"
	"net"
	"strconv"

	"github.com/Enelsep/42_TAP/core/protocol"
)

// Server owns the listening socket and the hub of connected clients.
type Server struct {
	addr string
	hub  *Hub
}

// New creates a Server that will listen on addr (e.g. ":4242").
func New(addr string) *Server {
	return &Server{addr: addr, hub: NewHub()}
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
			s.hub.Unregister(c)
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

		default:
			log.Printf("tap server: %s -> %+v", remote, cmd)
			c.send(protocol.FormatOK(""))
		}
	}
}

// handleConnect claims a name in the hub or replies 201 NAME_IN_USE.
func (s *Server) handleConnect(c *Client, cmd protocol.Command) {
	if c.name != "" {
		c.send(protocol.FormatErr(protocol.ErrNameInUse))
		return
	}
	c.name = cmd.Arg
	if !s.hub.Register(c) {
		c.name = ""
		c.send(protocol.FormatErr(protocol.ErrNameInUse))
		return
	}
	log.Printf("tap server: %s is now %s", c.conn.RemoteAddr(), c.name)
	c.send(protocol.FormatOK("connected"))
	s.broadcastStats()
}

// broadcastStats pushes the current player count to every connected client.
func (s *Server) broadcastStats() {
	s.hub.Broadcast(protocol.FormatEvent(protocol.Event{
		Scope:   protocol.EvtStats,
		Players: s.hub.Count(),
	}))
}
