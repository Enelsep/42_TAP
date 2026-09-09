// Command cli is the server's client (T5.1 + T5.2): a translating
// interface over a raw TCP connection. Typed input goes through
// translateInput (a handful of natural phrasings — "go north", "say hi" —
// onto their RFC verb) and everything received goes through renderer.line
// (JSON payloads and events rendered readably, with ANSI colors). Anything
// neither one specifically recognizes — including the full RFC syntax
// typed directly — passes through unchanged in both directions, so this
// never drifts from what the server actually speaks. -raw restores T5.1's
// original behavior verbatim, for testing against the wire itself.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/Enelsep/42_TAP/core/protocol"
)

func main() {
	addr := flag.String("addr", "localhost:4241", "server address")
	raw := flag.Bool("raw", false, "T5.1 mode: no translation or rendering, everything verbatim")
	flag.Parse()

	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tap cli:", err)
		os.Exit(1)
	}
	defer conn.Close()

	render := newRenderer()
	done := make(chan struct{})

	// Socket -> stdout: prints replies and events the instant they arrive,
	// independent of whatever the user is mid-typing on the next line —
	// the whole "stay responsive to async events" requirement (roadmap §5).
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			if *raw {
				fmt.Println(line)
				continue
			}
			fmt.Println(render.line(line))
		}
	}()

	// Stdin -> socket: translated (unless -raw), then sent exactly as
	// produced — translateInput's own fallback is "return line unchanged",
	// so this never needs a second raw/non-raw branch of its own logic.
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := scanner.Text()
			if !*raw {
				line = translateInput(line)
				if cmd, err := protocol.ParseCommand(line); err == nil {
					render.expect(cmd.Verb)
				}
			}
			if _, err := io.WriteString(conn, line+"\n"); err != nil {
				return
			}
		}
		// stdin closed (EOF/Ctrl-D, or all of it already consumed when
		// piped): half-close the write side only. A full Close here would
		// race the socket->stdout goroutine above and could drop whatever
		// the server is still in the middle of sending back — QUIT's own
		// "OK bye" included. The server closing its end once it's done is
		// what actually ends the program, via that goroutine's Scan
		// hitting EOF.
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			conn.Close()
		}
	}()

	<-done
}
