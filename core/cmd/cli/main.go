// Command cli is the server's client (T5.1 + T5.2): typed input goes
// through translateInput, incoming lines through renderer.line. Anything
// neither recognizes — full RFC syntax included — passes through
// unchanged. -raw restores T5.1's verbatim behavior for wire-level testing.
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

	// Socket -> stdout: prints replies/events the instant they arrive,
	// independent of whatever the user is mid-typing (roadmap §5).
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
				// The server replies to every line, parsable or not, so
				// expect must fire unconditionally too — skipping it on a
				// local parse failure desyncs the queue with every later
				// reply. On failure cmd is the zero Command; its empty Verb
				// falls back to raw rendering, which is correct here.
				cmd, _ := protocol.ParseCommand(line)
				render.expect(cmd.Verb)
			}
			if _, err := io.WriteString(conn, line+"\n"); err != nil {
				return
			}
		}
		// stdin closed: half-close the write side only. A full Close here
		// could race the socket->stdout goroutine and drop the server's
		// final reply; that goroutine's own Scan hitting EOF is what ends
		// the program.
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			conn.Close()
		}
	}()

	<-done
}
