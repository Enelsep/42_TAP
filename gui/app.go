package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Enelsep/42_TAP/core/protocol"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	dialTimeout  = 5 * time.Second
	replyTimeout = 5 * time.Second

	EventTAP  = "tap:evt"
	EventGone = "tap:disconnected"
)

var errNotConnected = errors.New("not connected")

// App is the Wails backend. It owns the TCP connection and is the only place
// in the GUI that speaks TAP: the frontend calls the bound methods below and
// renders the typed values they return, and never sees a wire line.
type App struct {
	ctx context.Context

	mu      sync.Mutex // guards conn/replies and serialises whole round-trips
	conn    net.Conn
	replies chan protocol.Reply
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) shutdown(context.Context) {
	a.Disconnect()
}

// --- connection ---

func (a *App) Connect(addr, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return errors.New("already connected")
	}
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(replyTimeout))
	greeting, err := reader.ReadString('\n')

	if err != nil {
		conn.Close()
		return fmt.Errorf("no greeting from %s: %w", addr, err)
	}
	conn.SetReadDeadline(time.Time{})

	if reply, err := protocol.ParseReply(greeting); err != nil || !reply.OK() {
		conn.Close()
		return fmt.Errorf("unexpected greeting %q", strings.TrimSpace(greeting))
	}

	a.conn = conn
	a.replies = make(chan protocol.Reply, 1)
	go a.readLoop(conn, reader, a.replies)

	reply, err := a.exchange(protocol.FormatCommand(protocol.Command{
		Verb: protocol.VerbConnect,
		Arg:  name,
	}))
	if err == nil && reply.Err != nil {
		err = reply.Err // 201 NAME_IN_USE, most likely
	}
	if err != nil {
		conn.Close()
		a.conn, a.replies = nil, nil
		return err
	}
	return nil
}

func (a *App) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn == nil {
		return nil
	}
	conn := a.conn
	_, err := a.exchange(protocol.FormatCommand(protocol.Command{Verb: protocol.VerbQuit}))
	conn.Close()
	a.conn, a.replies = nil, nil
	return err
}

func (a *App) Connected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}

func (a *App) readLoop(conn net.Conn, reader *bufio.Reader, replies chan protocol.Reply) {
	defer func() {
		close(replies)
		conn.Close()

		a.mu.Lock()
		live := a.conn == conn // false if Connect or Disconnect already cleaned up
		if live {
			a.conn, a.replies = nil, nil
		}
		a.mu.Unlock()

		if live {
			a.emit(EventGone)
		}
	}()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()

		if protocol.IsEvent(line) {
			event, err := protocol.ParseEvent(line)
			if err != nil {
				continue // malformed event: ignore it, never drop the session
			}
			a.emit(EventTAP, event)
			continue
		}
		reply, err := protocol.ParseReply(line)
		if err != nil {
			continue
		}
		select {
		case replies <- reply:
		default: // nobody waiting (a timed-out command): drop it
		}
	}
}

func (a *App) emit(name string, data ...any) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, name, data...)
	}
}

// --- command plumbing ---

// exchange writes one command and waits for its reply. a.mu must be held,
// which is what keeps two commands from interleaving on the socket.
func (a *App) exchange(line string) (protocol.Reply, error) {
	if a.conn == nil {
		return protocol.Reply{}, errNotConnected
	}
	// Discard a reply that arrived after its command timed out, so it can
	// never be mistaken for the answer to this one.
	select {
	case <-a.replies:
	default:
	}

	if _, err := io.WriteString(a.conn, line); err != nil {
		return protocol.Reply{}, err
	}
	select {
	case reply, ok := <-a.replies:
		if !ok {
			return protocol.Reply{}, errNotConnected
		}
		return reply, nil
	case <-time.After(replyTimeout):
		return protocol.Reply{}, fmt.Errorf("no reply within %s", replyTimeout)
	}
}

// command runs one round-trip and turns an ERR reply into a Go error, which
// Wails hands the frontend as a rejected promise carrying "<code> <symbol>".
func (a *App) command(cmd protocol.Command) (string, error) {
	a.mu.Lock()
	reply, err := a.exchange(protocol.FormatCommand(cmd))
	a.mu.Unlock()

	if err != nil {
		return "", err
	}
	if reply.Err != nil {
		return "", reply.Err
	}
	return reply.Data, nil
}

// value runs cmd and strips the "key=" prefix the RFC puts on some replies
// (room=, taken=, group=…). A reply without the prefix is returned as-is.
func (a *App) value(cmd protocol.Command, key string) (string, error) {
	data, err := a.command(cmd)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(data, key+"="), nil
}

// decode runs cmd and unmarshals its JSON payload.
func decode[T any](a *App, cmd protocol.Command) (T, error) {
	var out T
	data, err := a.command(cmd)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal([]byte(data), &out); err != nil {
		return out, fmt.Errorf("bad %s payload %q: %w", cmd.Verb, data, err)
	}
	return out, nil
}

// Send is the escape hatch for a raw command line, used by a console pane or
// for debugging. The line is parsed before it goes out, so the GUI can still
// only ever emit valid RFC commands (§2.6's interoperability rule).
func (a *App) Send(raw string) (string, error) {
	cmd, err := protocol.ParseCommand(raw)
	if err != nil {
		return "", err
	}
	return a.command(cmd)
}

// --- typed helpers, one per RFC command ---

func (a *App) Look() (protocol.LookReply, error) {
	return decode[protocol.LookReply](a, protocol.Command{Verb: protocol.VerbLook})
}

// Move returns the id of the room entered.
func (a *App) Move(dir string) (string, error) {
	return a.value(protocol.Command{Verb: protocol.VerbMove, Arg: dir}, "room")
}

func (a *App) Chat(scope, message string) error {
	s := protocol.ChatScope(strings.ToUpper(scope))
	switch s {
	case protocol.ChatGlobal, protocol.ChatRoom, protocol.ChatGroup:
	default:
		return fmt.Errorf("unknown chat scope %q", scope)
	}
	_, err := a.command(protocol.Command{Verb: protocol.VerbChat, Scope: s, Arg: message})
	return err
}

func (a *App) Who() (int, error) {
	data, err := a.value(protocol.Command{Verb: protocol.VerbWho}, "players")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(data))
}

func (a *App) GroupCreate() (string, error) {
	return a.value(protocol.Command{Verb: protocol.VerbGroup, Sub: protocol.GroupCreate}, "group")
}

func (a *App) GroupInvite(player string) error {
	_, err := a.command(protocol.Command{Verb: protocol.VerbGroup, Sub: protocol.GroupInvite, Arg: player})
	return err
}

func (a *App) GroupJoin(group string) (string, error) {
	return a.value(protocol.Command{Verb: protocol.VerbGroup, Sub: protocol.GroupJoin, Arg: group}, "group")
}

func (a *App) GroupLeave() error {
	_, err := a.command(protocol.Command{Verb: protocol.VerbGroup, Sub: protocol.GroupLeave})
	return err
}

func (a *App) Take(item string) (string, error) {
	return a.value(protocol.Command{Verb: protocol.VerbTake, Arg: item}, "taken")
}

func (a *App) Drop(item string) (string, error) {
	return a.value(protocol.Command{Verb: protocol.VerbDrop, Arg: item}, "dropped")
}

func (a *App) Inventory() ([]string, error) {
	return decode[[]string](a, protocol.Command{Verb: protocol.VerbInventory})
}

// Talk returns the NPC's line as plain text: §5.4.4 replies "OK <dialogue>",
// not JSON, whatever the subject PDF's example shows.
func (a *App) Talk(npc string) (string, error) {
	return a.command(protocol.Command{Verb: protocol.VerbTalk, Arg: npc})
}

func (a *App) Attack(npc string) (protocol.AttackReply, error) {
	return decode[protocol.AttackReply](a, protocol.Command{Verb: protocol.VerbAttack, Arg: npc})
}

func (a *App) Status() (protocol.StatusReply, error) {
	return decode[protocol.StatusReply](a, protocol.Command{Verb: protocol.VerbStatus})
}

func (a *App) Quest(npc string) (protocol.QuestReply, error) {
	return decode[protocol.QuestReply](a, protocol.Command{Verb: protocol.VerbQuest, Arg: npc})
}

func (a *App) Quests() (protocol.QuestsReply, error) {
	return decode[protocol.QuestsReply](a, protocol.Command{Verb: protocol.VerbQuests})
}
