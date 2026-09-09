// Command cli is the server's dev tool (T5.1): a raw pass-through TCP
// client. It speaks no protocol of its own — every line typed goes out
// verbatim, every line received is printed verbatim — so RFC syntax works
// exactly as written and nothing here can drift from what the server
// actually emits. T5.2 layers a translating interface on top of this.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
)

func main() {
	addr := flag.String("addr", "localhost:4241", "server address")
	flag.Parse()

	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tap cli:", err)
		os.Exit(1)
	}
	defer conn.Close()

	done := make(chan struct{})

	// Socket -> stdout: prints replies and events the instant they arrive,
	// independent of whatever the user is mid-typing on the next line —
	// the whole "stay responsive to async events" requirement (roadmap §5).
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
	}()

	// Stdin -> socket: every line goes out exactly as typed.
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if _, err := io.WriteString(conn, scanner.Text()+"\n"); err != nil {
				return
			}
		}
		conn.Close() // stdin closed (EOF/Ctrl-D): stop the socket reader too
	}()

	<-done
}
