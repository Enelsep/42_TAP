package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Enelsep/42_TAP/core/protocol"
)

// renderer turns wire lines into readable ones (T5.2). A reply carries no
// verb of its own, so expect records each command's verb as it goes out,
// and reply pops the oldest one for the next non-EVT line back — wire
// order is FIFO on both sides, so a plain queue is enough.
type renderer struct {
	pending chan protocol.Verb
}

func newRenderer() *renderer {
	return &renderer{pending: make(chan protocol.Verb, 64)}
}

// expect records that a command with verb v was just sent.
func (r *renderer) expect(v protocol.Verb) {
	select {
	case r.pending <- v:
	default: // 64 outstanding commands with no reply yet — drop the pairing, not the typing
	}
}

// line renders one line received from the server. It never errors:
// anything unrecognized (D8's tolerance policy) prints as it arrived —
// the same "pass it through raw" principle translateInput applies inbound.
func (r *renderer) line(raw string) string {
	if protocol.IsEvent(raw) {
		return r.renderEvent(raw)
	}
	return r.renderReply(raw)
}

func (r *renderer) renderReply(raw string) string {
	if raw == protocol.Greeting {
		return colorize(ansiBold, "connected — TAP protocol v1")
	}

	reply, err := protocol.ParseReply(raw)
	if err != nil {
		return raw
	}

	var verb protocol.Verb
	select {
	case verb = <-r.pending:
	default:
	}

	if !reply.OK() {
		return colorize(ansiRed, fmt.Sprintf("! %d %s", reply.Err.Code, reply.Err.Symbol))
	}

	switch verb {
	case protocol.VerbLook:
		if s, ok := renderLook(reply.Data); ok {
			return s
		}
	case protocol.VerbInventory:
		if s, ok := renderInventory(reply.Data); ok {
			return s
		}
	case protocol.VerbStatus:
		if s, ok := renderStatus(reply.Data); ok {
			return s
		}
	case protocol.VerbAttack:
		if s, ok := renderAttack(reply.Data); ok {
			return s
		}
	case protocol.VerbFlee:
		if s, ok := renderFlee(reply.Data); ok {
			return s
		}
	case protocol.VerbQuest:
		if s, ok := renderQuest(reply.Data); ok {
			return s
		}
	case protocol.VerbQuests:
		if s, ok := renderQuests(reply.Data); ok {
			return s
		}
	case protocol.VerbTalk:
		return colorize(ansiCyan, reply.Data)
	case protocol.VerbMove:
		if _, val, ok := strings.Cut(reply.Data, "="); ok {
			return "→ moved to " + colorize(ansiCyan, val)
		}
	case protocol.VerbTake:
		if _, val, ok := strings.Cut(reply.Data, "="); ok {
			return "picked up " + colorize(ansiYellow, val)
		}
	case protocol.VerbDrop:
		if _, val, ok := strings.Cut(reply.Data, "="); ok {
			return "dropped " + colorize(ansiYellow, val)
		}
	case protocol.VerbWho:
		if _, val, ok := strings.Cut(reply.Data, "="); ok {
			return colorize(ansiDim, val+" player(s) online")
		}
	case protocol.VerbGroup:
		if _, val, ok := strings.Cut(reply.Data, "="); ok {
			return "group: " + colorize(ansiMagenta, val)
		}
	}

	if reply.Data == "" {
		return colorize(ansiGreen, "OK")
	}
	return reply.Data
}

func renderLook(data string) (string, bool) {
	var l protocol.LookReply
	if json.Unmarshal([]byte(data), &l) != nil {
		return "", false
	}

	var b strings.Builder
	fmt.Fprintln(&b, colorize(ansiBold+ansiCyan, l.Room.Name))
	fmt.Fprintln(&b, l.Room.Description)
	if len(l.Players) > 0 {
		fmt.Fprintf(&b, "%s %s\n", colorize(ansiDim, "players:"), strings.Join(l.Players, ", "))
	}
	if len(l.NPCs) > 0 {
		fmt.Fprintf(&b, "%s %s\n", colorize(ansiMagenta, "npcs:"), strings.Join(l.NPCs, ", "))
	}
	if len(l.Items) > 0 {
		fmt.Fprintf(&b, "%s %s\n", colorize(ansiYellow, "items:"), strings.Join(l.Items, ", "))
	}
	if len(l.Room.Exits) > 0 {
		dirs := make([]string, 0, len(l.Room.Exits))
		for dir, to := range l.Room.Exits {
			dirs = append(dirs, dir+"→"+to)
		}
		sort.Strings(dirs)
		fmt.Fprintf(&b, "%s %s", colorize(ansiGreen, "exits:"), strings.Join(dirs, "  "))
	}
	return strings.TrimRight(b.String(), "\n"), true
}

func renderInventory(data string) (string, bool) {
	var inv protocol.InventoryReply
	if json.Unmarshal([]byte(data), &inv) != nil {
		return "", false
	}
	if len(inv) == 0 {
		return colorize(ansiDim, "(empty)"), true
	}
	return colorize(ansiYellow, strings.Join(inv, ", ")), true
}

// statusColor keys off the same Status* constants combat replies share
// (AttackReply, FleeReply, StatusReply) — one mapping for all three.
func statusColor(status string) string {
	switch status {
	case protocol.StatusHealthy:
		return ansiGreen
	case protocol.StatusCombat:
		return ansiYellow
	case protocol.StatusDead:
		return ansiRed
	default:
		return ansiReset
	}
}

func renderStatus(data string) (string, bool) {
	var s protocol.StatusReply
	if json.Unmarshal([]byte(data), &s) != nil {
		return "", false
	}
	return fmt.Sprintf("%s (%s)", colorize(statusColor(s.Status), fmt.Sprintf("%d/%d hp", s.HP, s.MaxHP)), s.Status), true
}

func renderAttack(data string) (string, bool) {
	var a protocol.AttackReply
	if json.Unmarshal([]byte(data), &a) != nil {
		return "", false
	}
	return fmt.Sprintf("hit for %s — target at %d hp, you at %s",
		colorize(ansiRed, fmt.Sprintf("%d", a.Damage)), a.TargetHP,
		colorize(statusColor(a.Status), fmt.Sprintf("%d hp (%s)", a.AttackerHP, a.Status))), true
}

func renderFlee(data string) (string, bool) {
	var f protocol.FleeReply
	if json.Unmarshal([]byte(data), &f) != nil {
		return "", false
	}
	if f.Damage == 0 {
		return fmt.Sprintf("fled to %s, unscathed", colorize(ansiCyan, f.Room)), true
	}
	return fmt.Sprintf("fled to %s, took %s on the way out — %s", colorize(ansiCyan, f.Room),
		colorize(ansiRed, fmt.Sprintf("%d", f.Damage)),
		colorize(statusColor(f.Status), fmt.Sprintf("%d hp (%s)", f.HP, f.Status))), true
}

func renderQuest(data string) (string, bool) {
	var q protocol.QuestReply
	if json.Unmarshal([]byte(data), &q) != nil {
		return "", false
	}
	return fmt.Sprintf("%s [%s]\n%s\nreward: %s", colorize(ansiBold, q.QuestID),
		colorize(questColor(q.Status), q.Status), q.Description, colorize(ansiYellow, q.Reward)), true
}

func renderQuests(data string) (string, bool) {
	var qs protocol.QuestsReply
	if json.Unmarshal([]byte(data), &qs) != nil {
		return "", false
	}
	if len(qs) == 0 {
		return colorize(ansiDim, "(no quests)"), true
	}
	var b strings.Builder
	for i, q := range qs {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s [%s] %s", q.QuestID, colorize(questColor(q.Status), q.Status), q.Progress)
	}
	return b.String(), true
}

func questColor(status string) string {
	if status == protocol.QuestCompleted {
		return ansiGreen
	}
	return ansiYellow // available/active
}

func (r *renderer) renderEvent(raw string) string {
	e, err := protocol.ParseEvent(raw)
	if err != nil {
		return raw
	}

	if e.Scope == protocol.EvtStats {
		return colorize(ansiDim, fmt.Sprintf("[%d players online]", e.Players))
	}

	switch e.Kind {
	case protocol.KindPresence:
		if e.Presence == protocol.PresenceLeave {
			return colorize(ansiDim, "← "+e.Player+" left")
		}
		return colorize(ansiGreen, "→ "+e.Player+" entered")
	case protocol.KindChat:
		return fmt.Sprintf("%s %s: %s", colorize(scopeColor(e.Scope), "["+string(e.Scope)+"]"), colorize(ansiBold, e.Player), e.Message)
	case protocol.KindInvite:
		return colorize(ansiMagenta, e.Player+" invited you to their group")
	case protocol.KindJoin:
		return colorize(ansiMagenta, e.Player+" joined the group")
	case protocol.KindLeave:
		return colorize(ansiDim, e.Player+" left the group")
	case protocol.KindNPCDeath:
		return colorize(ansiRed, e.NPC+" has died")
	default:
		return raw // unrecognized kind (D8): show it, don't hide it
	}
}

func scopeColor(scope protocol.EventScope) string {
	switch scope {
	case protocol.EvtGlobal:
		return ansiBlue
	case protocol.EvtGroup:
		return ansiMagenta
	default: // EvtRoom
		return ansiCyan
	}
}
