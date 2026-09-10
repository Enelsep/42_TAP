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

	// Socket -> stdout:
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

	// Stdin -> socket:
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := scanner.Text()
			if !*raw {
				line = translateInput(line)
				cmd, _ := protocol.ParseCommand(line)
				render.expect(cmd.Verb)
			}
			if _, err := io.WriteString(conn, line+"\n"); err != nil {
				return
			}
		}
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			conn.Close()
		}
	}()

	<-done
}
