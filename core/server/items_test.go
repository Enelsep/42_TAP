package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enelsep/42_TAP/core/protocol"
	"github.com/Enelsep/42_TAP/core/world"
)

// --- harness ---

func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	w, err := world.Load("../../data/world.json")
	if err != nil {
		t.Fatalf("load world: %v", err)
	}
	if err := w.Validate(); err != nil {
		t.Fatalf("validate world: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	s := New(addr, w)
	go s.Run()
	for range 100 {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return s, addr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("server never came up on %s", addr)
	return nil, ""
}

// census counts every existing instance of every item id: what is on a floor
// plus what a connected player carries. Items held by a client that has
// disconnected are deliberately not counted separately — Unregister is
// supposed to have put them back on a floor, and a missing item here is as
// much a bug as a duplicated one.
func (s *Server) census() map[string]int {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()

	n := map[string]int{}
	for _, ids := range s.hub.roomItems {
		for _, id := range ids {
			n[id]++
		}
	}
	for _, c := range s.hub.clients {
		for id := range c.inventory {
			n[id]++
		}
	}
	return n
}

// assertUnique is the §8.1 invariant: no item id may exist twice at once,
// anywhere in the world. step names the point in the scenario, so a failure
// says which action broke it rather than only that something did.
func assertUnique(t *testing.T, s *Server, step string) {
	t.Helper()
	seen := s.census()
	for _, id := range slices.Sorted(maps.Keys(seen)) {
		if n := seen[id]; n > 1 {
			t.Errorf("after %s: %s exists %d times, items are unique instances", step, id, n)
		}
	}

	// The hub's ledger of what exists is what stops a quest from spawning a
	// second copy, so it has to agree with what actually exists: an id left
	// in it after its item was consumed blocks the item forever, and one
	// missing from it lets the next grant mint a duplicate.
	s.hub.mu.Lock()
	ledger := maps.Clone(s.hub.spawnedItems)
	s.hub.mu.Unlock()
	for _, id := range slices.Sorted(maps.Keys(ledger)) {
		if seen[id] == 0 {
			t.Errorf("after %s: %s is marked as existing but is nowhere in the world", step, id)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(seen)) {
		if !ledger[id] {
			t.Errorf("after %s: %s exists but is not in the hub ledger, a grant could duplicate it", step, id)
		}
	}
}

type player struct {
	t    *testing.T
	name string
	conn net.Conn
	r    *bufio.Reader
}

func connect(t *testing.T, addr, name string) *player {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	p := &player{t: t, name: name, conn: conn, r: bufio.NewReader(conn)}
	p.readLine() // greeting
	p.ok("CONNECT " + name)
	return p
}

func (p *player) readLine() string {
	p.t.Helper()
	p.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := p.r.ReadString('\n')
	if err != nil {
		p.t.Fatalf("%s: read: %v", p.name, err)
	}
	return strings.TrimRight(line, "\r\n")
}

// do sends one command and returns its reply, skipping the events that may
// be queued ahead of it — a client's outbound queue is ordered, so the first
// non-EVT line after the write is this command's own reply.
func (p *player) do(cmd string) string {
	p.t.Helper()
	if _, err := io.WriteString(p.conn, cmd+protocol.LineTerm); err != nil {
		p.t.Fatalf("%s: write: %v", p.name, err)
	}
	for {
		if line := p.readLine(); !strings.HasPrefix(line, "EVT ") {
			return line
		}
	}
}

// ok runs cmd and returns the reply's data, failing the test on ERR.
func (p *player) ok(cmd string) string {
	p.t.Helper()
	reply := p.do(cmd)
	if !strings.HasPrefix(reply, "OK") {
		p.t.Fatalf("%s: %q -> %s", p.name, cmd, reply)
	}
	return strings.TrimPrefix(strings.TrimPrefix(reply, "OK"), " ")
}

func (p *player) walk(dirs ...string) {
	p.t.Helper()
	for _, dir := range dirs {
		p.ok("MOVE " + dir)
	}
}

func (p *player) inventory() []string {
	p.t.Helper()
	var items []string
	if err := json.Unmarshal([]byte(p.ok("INVENTORY")), &items); err != nil {
		p.t.Fatalf("%s: bad INVENTORY payload: %v", p.name, err)
	}
	return items
}

func (p *player) holds(item string) bool {
	p.t.Helper()
	return slices.Contains(p.inventory(), item)
}

// killHunter attacks until the hunter is down, and reports how many rounds it
// took so a scenario can tell "already dead" from "never died".
func (p *player) killHunter() int {
	p.t.Helper()
	for round := 1; round <= 20; round++ {
		reply := p.do("ATTACK npc.hunter")
		if !strings.HasPrefix(reply, "OK") {
			p.t.Fatalf("%s: ATTACK round %d -> %s", p.name, round, reply)
		}
		var hit protocol.AttackReply
		if err := json.Unmarshal([]byte(strings.TrimPrefix(reply, "OK ")), &hit); err != nil {
			p.t.Fatalf("%s: bad ATTACK payload: %v", p.name, err)
		}
		if hit.TargetHP == 0 {
			return round
		}
	}
	p.t.Fatalf("%s: hunter still standing after 20 rounds", p.name)
	return 0
}

// routes through the world, from the start room.
var (
	toBar     = []string{"east", "east", "east"}
	toShop    = []string{"east", "east", "south"}
	toNest    = []string{"east", "east", "north", "north"}
	toSuburbs = []string{"north"}
)

// --- scenarios ---

// TestDeliverQuestKeepsItemsUnique walks the bone quest with two players who
// both hold it, which is the case where a per-player grant would mint a
// second bone.
func TestDeliverQuestKeepsItemsUnique(t *testing.T) {
	s, addr := startTestServer(t)
	alice := connect(t, addr, "alice")
	bob := connect(t, addr, "bob")

	alice.walk(toBar...)
	bob.walk(toBar...)
	alice.ok("QUEST npc.barman")
	assertUnique(t, s, "alice accepted the bone quest")
	bob.ok("QUEST npc.barman")
	assertUnique(t, s, "bob accepted the same quest")

	if !alice.holds("item.bone") {
		t.Fatal("alice accepted the deliver quest but was never handed the bone")
	}
	if bob.holds("item.bone") {
		t.Error("bob holds a second bone: the quest grant minted a duplicate")
	}

	alice.walk("west", "west", "west", "north")
	alice.ok("TALK npc.dog")
	assertUnique(t, s, "alice delivered the bone")

	if alice.holds("item.bone") {
		t.Error("the delivered bone is still in alice's inventory")
	}
	if !alice.holds("item.crysknife") {
		t.Error("alice completed the delivery but got no reward")
	}

	// The bone is destroyed on delivery, so the id is free again: bob, who
	// has been carrying an unfulfillable quest, can now be handed one.
	bob.ok("QUEST npc.barman")
	assertUnique(t, s, "bob asked again after the bone was consumed")
	if !bob.holds("item.bone") {
		t.Error("bob is stuck with an active deliver quest and no bone to deliver")
	}

	// Only one crysknife will ever exist, so bob closes the quest unpaid.
	bob.walk("west", "west", "west", "north")
	bob.ok("TALK npc.dog")
	assertUnique(t, s, "bob delivered the second bone")
	if bob.holds("item.crysknife") {
		t.Error("bob got a second crysknife: the reward was duplicated")
	}
}

// TestKillQuestRewardsOnlyTheKiller pits three holders of the same kill quest
// against one hunter. Every holder's quest closes (D15 shares kill credit),
// but the water is a unique instance and must land on whoever struck it down.
func TestKillQuestRewardsOnlyTheKiller(t *testing.T) {
	s, addr := startTestServer(t)
	// idle1 connects first on purpose: the reward used to go to whoever the
	// client map happened to yield first, which is insertion order in
	// practice — a bystander who logged in earlier reliably stole it.
	idle1 := connect(t, addr, "idle1")
	idle2 := connect(t, addr, "idle2")
	killer := connect(t, addr, "killer")

	for _, p := range []*player{killer, idle1, idle2} {
		p.walk(toShop...)
		p.ok("QUEST npc.vendor")
	}
	assertUnique(t, s, "three players took the kill quest")

	killer.walk("north", "north", "north")
	killer.killHunter()
	assertUnique(t, s, "the hunter died")

	if !killer.holds("item.water") {
		t.Errorf("the killer did not get the reward; inventory=%v", killer.inventory())
	}
	for _, p := range []*player{idle1, idle2} {
		if p.holds("item.water") {
			t.Errorf("%s got the reward without landing a blow", p.name)
		}
	}

	// Everyone's quest still closes, reward or not.
	for _, p := range []*player{killer, idle1, idle2} {
		var quests []protocol.QuestEntry
		if err := json.Unmarshal([]byte(p.ok("QUESTS")), &quests); err != nil {
			t.Fatalf("%s: bad QUESTS payload: %v", p.name, err)
		}
		if len(quests) != 1 || quests[0].Status != protocol.QuestCompleted {
			t.Errorf("%s: quests = %+v, want the kill quest completed", p.name, quests)
		}
	}
}

// TestDropsAndDisconnectKeepItemsUnique covers the two paths that put an item
// back on a floor: an explicit DROP, and the inventory spill on disconnect.
func TestDropsAndDisconnectKeepItemsUnique(t *testing.T) {
	s, addr := startTestServer(t)
	alice := connect(t, addr, "alice")
	bob := connect(t, addr, "bob")

	alice.walk(toBar...)
	bob.walk(toBar...)
	alice.ok("TAKE item.liquor")
	if reply := bob.do("TAKE item.liquor"); !strings.HasPrefix(reply, "ERR") {
		t.Errorf("bob took the liquor alice is holding: %s", reply)
	}
	assertUnique(t, s, "two players raced for the liquor")

	alice.ok("DROP item.liquor")
	bob.ok("TAKE item.liquor")
	assertUnique(t, s, "the liquor changed hands")

	// bob quits carrying it: the liquor must survive on the bar floor, once.
	bob.ok("QUIT")
	bob.conn.Close()
	for range 100 { // the drop happens in the connection's own goroutine
		if s.census()["item.liquor"] == 1 && s.hub.Count() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assertUnique(t, s, "bob quit holding the liquor")
	if n := s.census()["item.liquor"]; n != 1 {
		t.Errorf("the liquor exists %d times after its holder disconnected, want 1", n)
	}
	alice.ok("TAKE item.liquor")
	assertUnique(t, s, "alice picked the spilled liquor back up")
}

// TestKeyStaysUniqueThroughTheGate follows the hunter's key from drop to the
// gated exit, including a second player trying to take it after the first.
func TestKeyStaysUniqueThroughTheGate(t *testing.T) {
	s, addr := startTestServer(t)
	alice := connect(t, addr, "alice")
	bob := connect(t, addr, "bob")

	alice.walk(toNest...)
	bob.walk(toNest...)
	alice.killHunter()
	assertUnique(t, s, "the hunter dropped its key")

	alice.ok("TAKE item.key")
	if reply := bob.do("TAKE item.key"); !strings.HasPrefix(reply, "ERR") {
		t.Errorf("bob took a second key: %s", reply)
	}
	assertUnique(t, s, "both players reached for the key")

	// The gate consumes nothing: walking through must not destroy the key.
	alice.walk("south", "west", "north")
	if !alice.holds("item.key") {
		t.Error("the key vanished on the way through the gate")
	}
	assertUnique(t, s, "alice opened the gate")

	// bob cannot follow without it.
	bob.walk("south", "west")
	if reply := bob.do("MOVE north"); !strings.HasPrefix(reply, "ERR") {
		t.Errorf("bob passed the gated exit with no key: %s", reply)
	}
}

// TestConcurrentTakeDropStaysUnique hammers one item with several players at
// once: the census afterwards is what proves TAKE/DROP resolve under a single
// lock rather than racing.
func TestConcurrentTakeDropStaysUnique(t *testing.T) {
	s, addr := startTestServer(t)

	const players, rounds = 6, 40
	ready := make(chan *player, players)
	for i := range players {
		p := connect(t, addr, fmt.Sprintf("racer%d", i))
		p.walk(toShop...)
		ready <- p
	}
	close(ready)

	done := make(chan struct{})
	for p := range ready {
		go func(p *player) {
			defer func() { done <- struct{}{} }()
			for range rounds {
				if strings.HasPrefix(p.do("TAKE item.spice"), "OK") {
					p.do("DROP item.spice")
				}
			}
		}(p)
	}
	for range players {
		<-done
	}
	assertUnique(t, s, "six players fought over the spice")
	if n := s.census()["item.spice"]; n != 1 {
		t.Errorf("the spice exists %d times after the scramble, want 1", n)
	}
}

// TestMain silences the server's slog output: these tests drive dozens of
// commands each, and the per-command log buries the assertion failures.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// TestConcurrentQuestAcceptGrantsOnce has several players accept the same
// deliver quest at the same instant: the granted bone is a single instance,
// so exactly one of them may walk away carrying it.
func TestConcurrentQuestAcceptGrantsOnce(t *testing.T) {
	s, addr := startTestServer(t)

	const players = 6
	ps := make([]*player, players)
	for i := range ps {
		ps[i] = connect(t, addr, fmt.Sprintf("seeker%d", i))
		ps[i].walk(toBar...)
	}

	start := make(chan struct{})
	done := make(chan bool, players)
	for _, p := range ps {
		go func(p *player) {
			<-start
			p.ok("QUEST npc.barman")
			done <- slices.Contains(p.inventory(), "item.bone")
		}(p)
	}
	close(start)

	carriers := 0
	for range players {
		if <-done {
			carriers++
		}
	}
	assertUnique(t, s, "six players accepted the bone quest at once")
	if carriers != 1 {
		t.Errorf("%d players walked away with the bone, want exactly 1", carriers)
	}
}

// TestConcurrentKillDropsOnce has two players finish the hunter off at the
// same time. Only one ATTACK may resolve the death, or the key is appended
// to the floor twice.
func TestConcurrentKillDropsOnce(t *testing.T) {
	s, addr := startTestServer(t)
	alice := connect(t, addr, "alice")
	bob := connect(t, addr, "bob")
	alice.walk(toNest...)
	bob.walk(toNest...)

	// Both hammer the hunter until it stops answering; whoever lands the
	// last blow wins, the other gets NPC_NOT_FOUND.
	start := make(chan struct{})
	done := make(chan struct{}, 2)
	for _, p := range []*player{alice, bob} {
		go func(p *player) {
			defer func() { done <- struct{}{} }()
			<-start
			for range 20 {
				if !strings.HasPrefix(p.do("ATTACK npc.hunter"), "OK") {
					return
				}
			}
		}(p)
	}
	close(start)
	<-done
	<-done

	assertUnique(t, s, "two players raced the hunter's last hit point")
	if n := s.census()["item.key"]; n != 1 {
		t.Errorf("the hunter dropped %d keys, want 1", n)
	}
}

// TestUncontractedKillPaysNobody pins the corner the reward rule creates: if
// whoever lands the killing blow never took the contract, the reward is
// never created at all. The holders' quests still close — the target is dead
// for good, so leaving them active would only be a quest they can never
// finish — but the single water instance stays unspawned rather than being
// handed to an arbitrary bystander.
func TestUncontractedKillPaysNobody(t *testing.T) {
	s, addr := startTestServer(t)
	holder := connect(t, addr, "holder")
	drifter := connect(t, addr, "drifter")

	holder.walk(toShop...)
	holder.ok("QUEST npc.vendor")

	drifter.walk(toNest...)
	drifter.killHunter()
	assertUnique(t, s, "an uncontracted player killed the hunter")

	if drifter.holds("item.water") {
		t.Error("the reward went to a player who never took the contract")
	}
	if holder.holds("item.water") {
		t.Error("the reward went to a holder who did not land the blow")
	}
	if n := s.census()["item.water"]; n != 0 {
		t.Errorf("the water exists %d times after an unearned kill, want 0", n)
	}
}
