# The Answer Protocol (TAP) — Architecture & Implementation Roadmap

> **Stack (locked):** Go · Wails v2 (GUI) · JSON world data · stdlib first (`net`, `bufio`, `encoding/json`, `log/slog`, `net/http`, `embed`)
> **Team:** Dev A (server + CLI client) · Dev B / Eliott (GUI client + world design) · EXEC (cheap execution model for well-scoped, mechanical tasks)
> **Spec:** RFC 42TAP (Dec 2024, Experimental) — digested in §2 below; keep the original in `docs/`.

---

## 1. Scope & goals

Build a small multiplayer text-adventure (MUD) played over the **line-based TCP protocol RFC 42TAP**. Deliverables:

| Deliverable | Description | Owner |
|---|---|---|
| **Server** | Implements *every* command and event of RFC 42TAP, loads world from JSON, validates it, item system, combat, quests, structured logging | Dev A |
| **CLI client** | Text client, stays responsive to async events while waiting for input | Dev A |
| **GUI client** | Wails app: room view, inventory, action buttons, chat vs log separation, player counters | Dev B |
| **World data** | JSON: ≥ 8 rooms (loop + branch), ≥ 3 NPC roles, ≥ 4 items (≥ 2 obtainable), ≥ 2 quests | Dev B |
| **Build tool** | Makefile: `deps`, `build`, `run`, `lint`, `clean` — documented in README | Dev A |
| **README** | Build docs + **every design choice justified** (combat, quests, CLI interface choice, protocol interpretations) | Both |

**Hard constraints to never lose sight of:**

- ⚠️ **Interchangeability:** your CLI/GUI clients must work against *other groups'* servers, and vice versa. Strict RFC compliance on both sides: emit exactly the RFC wire format, parse exactly the RFC wire format. No private extensions the other side depends on.
- Framing: TCP, UTF-8, one message per LF (`0x0A`)-terminated line. The RFC explicitly requires correct handling of **TCP fragmentation and coalescing** (§9.2) — a command may arrive split across packets or several commands in one packet. (`bufio.Scanner` solves both for free; just know *why* for the defense.)
- Graceful disconnects: remove player state **before** broadcasting the leave event; broadcasts must never be interrupted by a client dying mid-send. RFC §3.3: server must clean up on both QUIT and abrupt drops.
- Resource limits (RFC §9.4, SHOULD): **1024-byte line limit**, connection cap, chat-frequency limiting. Cheap to add, great defense material.
- No persistence required (state resets on restart) — don't build any.

**Explicitly out of scope (YAGNI):** persistence/DB, real authentication (RFC §9.1: username uniqueness *is* the auth), TLS, reconnection logic, status effects/buffs (RFC lists them as *optional* design space), scripting engine for NPCs. If neither the subject nor the RFC requires it, don't build it.

---

## 2. Protocol contract — RFC 42TAP digest

This section replaces guesswork. It is the executable spec for `protocol/` and its tests.

### 2.1 Session lifecycle

State machine: `DISCONNECTED → CONNECTED → AUTHENTICATED → TERMINATED`.

```
(TCP accept)
S: OK hello proto=1          ← server speaks FIRST (MUST, §3.2)
C: CONNECT alice
S: OK connected              (or: ERR 201 NAME_IN_USE)
...gameplay...
C: QUIT
S: OK bye
```

Commands before a successful CONNECT are invalid — the RFC defines no error code for this, so **document your choice** (proposal: reply `ERR 201`-style with a dedicated message, or simply an `ERR` with a sensible code; write it in README as an interpretation).

### 2.2 Message grammar (ABNF summary)

- `command-line = command-name [SP arguments] LF` — command names **case-insensitive** (§4.2): `move north` must work.
- `response-line = ("OK" / "ERR" SP code SP message) [SP data] LF`
- `event-line = "EVT" SP event-type SP event-data LF`

⚠️ **Spec quirk #1:** the CONNECT ABNF (§3.1) says `CRLF` while everything else says `LF`. Resolution: **emit LF, tolerate a trailing `\r` on receive** (strip it). One line of code, maximal interop, note it in README.

### 2.3 Command table (complete — this is the whole surface)

| Command | Syntax | Success reply | Errors |
|---|---|---|---|
| CONNECT | `CONNECT <username>` | `OK connected` | `201 NAME_IN_USE` |
| LOOK | `LOOK` | `OK <room-JSON>` (see 2.4) | — |
| MOVE | `MOVE <direction>` | `OK room=<room.id>` | `301 NO_EXIT` |
| QUIT | `QUIT` | `OK bye` | — |
| CHAT | `CHAT <GLOBAL\|ROOM\|GROUP> <message…>` | `OK` (+ EVT broadcast, sender included) | — |
| WHO | `WHO` | `OK players=<count>` ⚠️ see quirk #2 | — |
| GROUP CREATE | `GROUP CREATE` | `OK group=<id>` | `402 ALREADY_IN_GROUP` |
| GROUP INVITE | `GROUP INVITE <player>` | `OK` | `401 NOT_IN_GROUP` |
| GROUP JOIN | `GROUP JOIN <group>` | `OK group=<id>` | `402 ALREADY_IN_GROUP` |
| GROUP LEAVE | `GROUP LEAVE` | `OK` | `401 NOT_IN_GROUP` |
| TAKE | `TAKE <item-id-or-name…>` | `OK taken=<item.id>` | `404 ITEM_NOT_FOUND` |
| DROP | `DROP <item-id-or-name…>` | `OK dropped=<item.id>` | `404 ITEM_NOT_IN_INVENTORY` |
| INVENTORY | `INVENTORY` | `OK <JSON array of item ids>` | — |
| TALK | `TALK <npc>` | `OK <dialogue text>` | `404 NPC_NOT_FOUND` |
| ATTACK | `ATTACK <npc>` | `OK {"attacker_hp":…,"target_hp":…,"damage":…,"status":…}` | `404 NPC_NOT_FOUND`, `405 NPC_NOT_HOSTILE` |
| STATUS | `STATUS` | `OK {"hp":…,"max_hp":…,"status":"…"}` | — |
| QUEST | `QUEST <npc>` | `OK {"quest_id":…,"description":…,"reward":…,"status":…}` | `404 NPC_NOT_FOUND`, `406 NO_QUEST_AVAILABLE` |
| QUESTS | `QUESTS` | `OK [{"quest_id":…,"status":…,"progress":…},…]` | — |

⚠️ **Spec quirk #2 — WHO:** the subject PDF's example shows `WHO` returning `OK { "room": ["alice","bob"], "server": 5 }`, but the RFC (§5.2.2) specifies `OK players=<count>`. **The RFC wins** (it's the normative document and the interop contract), but note the conflict and your resolution in README — evaluators love seeing that you caught it. The GUI's "players in room" counter can be derived from LOOK's `players` array instead.

⚠️ **Spec quirk #3 — duplicated error code 404:** three distinct conditions (`ITEM_NOT_FOUND`, `ITEM_NOT_IN_INVENTORY`, `NPC_NOT_FOUND`) share code 404. Clients must therefore key on the **symbolic message**, not just the numeric code. Encode both in the `protocol` package's error type.

Other codes: `900 CONNECTION_FAILED`, `901 SEND_FAILED` (9xx = fatal, client should reconnect; 4xx = recoverable — RFC §7.3/8.2). These 9xx codes are mostly client-side diagnostics.

### 2.4 JSON payloads (define once in `protocol/`, share everywhere)

`LOOK` reply (exact shape, §5.1.2):

```json
{
  "room": {
    "id": "loc.bakery",
    "name": "Village Bakery",
    "description": "A warm bakery filled with the scent of fresh bread.",
    "exits": { "south": "loc.square" }
  },
  "players": ["alice"],
  "items": ["item.herbs"],
  "npcs": ["npc.baker"]
}
```

Plus: `INVENTORY` → `["item.herbs", "item.loaf_bread"]`; `ATTACK`/`STATUS`/`QUEST`/`QUESTS` payloads as in the table. Each gets a Go struct with json tags in `protocol/`; server marshals them, both clients unmarshal them. **JSON on one line** — the payload must never contain a raw newline or it breaks framing (Go's `json.Marshal` is newline-free by default; never use `json.MarshalIndent` on the wire).

### 2.5 Event table (complete)

| Event | Wire format |
|---|---|
| Room presence | `EVT ROOM PRESENCE ENTER <player>` / `EVT ROOM PRESENCE LEAVE <player>` |
| Room chat | `EVT ROOM CHAT <player> <message…>` |
| Global chat | `EVT GLOBAL CHAT <player> <message…>` |
| Group lifecycle | `EVT GROUP INVITE <player>` / `EVT GROUP JOIN <player>` / `EVT GROUP LEAVE <player>` |
| Group chat | `EVT GROUP CHAT <player> <message…>` |
| Player count | `EVT STATS players=<count>` |

Notes:
- The subject's example confirms the **sender also receives** the chat EVT after its `OK` — broadcast to everyone in scope, sender included.
- `EVT STATS players=` implies the server broadcasts the updated count on every connect/disconnect. Easy to forget; wire it into the connect/cleanup paths.
- ⚠️ **Spec quirk #4 — GROUP INVITE:** the event carries only `<player>`, not the group id. How the invitee learns *which* group to JOIN is underspecified. Proposal: `EVT GROUP INVITE <inviter>` where JOIN accepts the inviter's group implicitly, or include the group id as the RFC's `event-data` is free-form enough. Pick one, README it, and make your *clients* tolerant of trailing extra tokens so other groups' interpretations don't crash you.

### 2.6 Underspecified by design (RFC §6.1) — decisions you MUST document

The RFC explicitly delegates these, and **requires** justification in README:

- **Combat:** turn management, damage formulas, combat states, extra commands (DEFEND/FLEE/USE_ITEM), status effects → our proposal in T3.7 (synchronous turns, flat damage ± spread, DEFEND/FLEE only, no status effects).
- **Quests:** progression tracking, auto vs manual completion, rewards, chains, extra commands (COMPLETE_QUEST/ABANDON_QUEST) → our proposal in T2.3 (auto-completion on objective hooks, no chains, no extra commands — QUEST/QUESTS suffice).

Extra commands are *optional* — every one you add is a potential interop hazard (other groups' servers won't know it). Rule: **clients only ever send RFC commands by default**; extra commands, if any, live server-side only and are demonstrated with your own client during defense.

---

## 3. Repository layout

Wails wants to own its module root, and the server/CLI want a plain Go module. The clean stdlib-era answer is a **Go workspace** (`go.work`) with two modules sharing a `protocol` package:

```
tap/
├── go.work                  # workspace: use ./core ./gui
├── Makefile                 # the required "building tool"
├── README.md                # build docs + design-choice justifications
├── docs/
│   ├── rfc-42tap.html       # the protocol, versioned WITH the code
│   └── decisions.md         # running log of design choices → feeds README
├── data/
│   └── world.json           # world data (single file, schema below)
├── core/                    # module github.com/<you>/tap/core
│   ├── go.mod
│   ├── cmd/
│   │   ├── server/main.go   # flag parsing, wiring, listen loop
│   │   └── cli/main.go      # CLI client entry
│   ├── protocol/            # SHARED: parse/format RFC lines, payload structs, error codes
│   │   ├── protocol.go
│   │   ├── payloads.go      # LookReply, AttackReply, StatusReply, QuestInfo... (§2.4)
│   │   └── protocol_test.go
│   ├── world/               # JSON schema structs, loader, validator
│   │   ├── world.go
│   │   └── validate.go
│   └── server/              # game state, hub, command handlers, combat, quests
│       ├── server.go        # accept loop, per-conn goroutines
│       ├── hub.go           # registry + broadcast + STATS events
│       ├── handlers.go      # one func per command
│       ├── combat.go
│       └── quests.go
└── gui/                     # module: generated by `wails init`, imports core/protocol
    ├── go.mod
    ├── wails.json
    ├── app.go               # Go backend: owns the TCP conn, exposes bound methods
    ├── main.go
    └── frontend/            # JS/TS + HTML/CSS (pick the plain "vanilla" template)
```

Why this shape:

- `protocol/` is written **once** and imported by server, CLI, *and* the GUI's Go backend. One parser, one formatter, one set of payload structs and error codes — protocol bugs get fixed in one place. With the RFC now digested in §2, this package is essentially a transcription job: the single most valuable package in the repo.
- `go.work` lets `gui/` import `core/protocol` without publishing anything or fighting `replace` directives.
- No `pkg/`, no `internal/` yet — two modules and five packages is enough structure for this project size.

---

## 4. Bootstrapping (first hour, Go-newcomer friendly)

```bash
# 1. Install Go ≥ 1.22 → https://go.dev/dl/  (check: go version)

# 2. Repo + workspace
mkdir tap && cd tap && git init
mkdir -p core/cmd/server core/cmd/cli core/protocol core/world core/server data docs
cd core && go mod init github.com/<you>/tap/core && cd ..
go work init ./core

# 3. Wails (GUI dev only)
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails doctor                          # checks system deps (webkit2gtk on Linux, etc.)
wails init -n gui -t vanilla          # creates ./gui as its own module
go work use ./gui

# 4. Sanity check
cat > core/cmd/server/main.go <<'EOF'
package main

import "fmt"

func main() { fmt.Println("tap server: hello") }
EOF
go run ./core/cmd/server
```

**Makefile skeleton** (fulfils the "building tool" requirement):

```makefile
.PHONY: deps build server cli gui lint test clean

deps:        ## install/verify dependencies
	go mod download && cd gui && go mod download

build:       ## build server + cli binaries into ./bin
	go build -o bin/server ./core/cmd/server
	go build -o bin/cli    ./core/cmd/cli

server: build ; ./bin/server -addr :4242 -world data/world.json
cli:    build ; ./bin/cli    -addr localhost:4242

gui:         ## run GUI in dev mode (hot reload)
	cd gui && wails dev

lint:        ## required by subject: standard linting
	gofmt -l . && go vet ./core/...

test:
	go test -race ./core/...

clean:
	rm -rf bin gui/build
```

Go habits that replace whole toolchains you may know from Python: `gofmt` **is** the formatter (non-negotiable style), `go vet` **is** the linter, `go test` **is** the test runner, `log/slog` **is** structured logging. No dependencies needed for any of it.

---

## 5. Architecture in one page

**Concurrency model (server):** the simplest thing that is actually correct.

- **One goroutine per connection** reading lines with `bufio.Scanner` — this natively satisfies RFC §9.2's fragmentation/coalescing requirements. Set `scanner.Buffer(make([]byte, 2048), 2048)` to enforce the RFC's recommended 1024-byte-ish line limit; an over-long line is a protocol error, respond and/or drop.
- **One `sync.Mutex` around the entire game state** (players, rooms, items, NPCs, groups). Every command handler does `lock → mutate → collect messages to send → unlock → send`. At MUD scale a global lock is invisible; per-room locks are a deadlock farm you don't need. <!-- ponytail: global lock; shard only if a profiler ever complains -->
- **One writer goroutine per client** draining a buffered channel (`chan string, 64`). Broadcasters never write to sockets directly — they enqueue. A slow/dead client fills its own buffer and gets dropped; it can never block a broadcast. This is the mechanism that satisfies "broadcasts without interruption if a client disconnects mid-send".
- **Disconnect path (QUIT, EOF, or error — one shared cleanup func):** reader goroutine exits → lock state, remove player, build LEAVE + `EVT STATS players=` events, unlock, broadcast. Order guaranteed by construction, as RFC §3.3 requires for both graceful and abrupt termination.

**Protocol layer:** every inbound line goes through `protocol.Parse(line) (Cmd, error)` (case-insensitive verbs, `\r`-stripping, §2.2) and every outbound message through `protocol.FormatOK(...)` / `FormatErr(code, symbol)` / `FormatEvt(...)`. JSON payloads are the structs of §2.4, marshaled compact. Handlers never touch raw strings. Same functions reused by both clients to parse server output.

**GUI (Wails):** the Go side of the Wails app owns the `net.Conn`. Bound methods (`Connect(name)`, `Send(cmd)`) go JS→Go; a reader goroutine parses incoming lines with `protocol` and pushes typed events JS-ward via `runtime.EventsEmit(ctx, "tap:evt", parsed)`. The frontend is a dumb renderer of typed events — zero protocol knowledge in JavaScript.

**CLI:** two goroutines — one reads the socket and prints, one reads stdin and writes. That's the whole "responsive while receiving async events" requirement.

---

## 6. Ordered roadmap

Tasks are sequenced by dependency. `[EXEC]` marks tasks scoped tightly enough to delegate to a cheaper model — always with the relevant §2 excerpt pasted into the prompt and reviewed against `protocol_test.go`.

### Phase 0 — Foundations (Day 1, together)

**T0.1 — Repo bootstrap** · *Both*
Layout + workspace + Makefile from §4. CI optional; a single GitHub Action running `make lint test` is plenty.

**T0.2 — Ratify the protocol contract** · *Both* — *(mostly done: §2 of this doc)*
Read §2 against the original RFC together, confirm the four flagged quirks and your resolutions (LF/CRLF, WHO format, 404 disambiguation, GROUP INVITE semantics), and seed `docs/decisions.md` with them. 30 minutes now saves the integration week.

### Phase 1 — Protocol package (blocks everything; do first)

**T1.1 — `protocol`: types + parser + formatter + payload structs** · *Dev A, review Dev B*
Transcribe §2.3/§2.5 into consts and structs; §2.4 into `payloads.go`. `Parse` (tokenize a line → typed command: uppercase the verb, then per-verb arg rules), `Format*` (typed → wire line). Pure functions, no I/O, no state.
*Technical notes:* manual `strings.Cut` tokenizing beats regex — the grammar is "VERB [sub] args rest…". Traps: `CHAT ROOM hello world` and `TAKE Rusty Sword` have *rest-of-line* arguments that must not be split; `GROUP` has four subcommands; error type carries `(code int, symbol string)` because of quirk #3; strip trailing `\r` (quirk #1).

**T1.2 — `protocol_test.go`: table-driven round-trip tests** · *[EXEC]*
For every row of §2.3 and §2.5: `Parse(Format(x)) == x`, JSON payload marshal/unmarshal round-trips, plus malformed cases (empty line, unknown verb, missing args, lowercase verbs must pass, >1024-byte line, bare `GROUP`, `CHAT NOWHERE hi`). This file **is** the RFC in executable form — your interoperability insurance. Perfect EXEC task: mechanical, verifiable, high value.

### Phase 2 — World (unblocks both server and world design)

**T2.1 — JSON schema + Go structs + loader** · *Dev B*
Adapt the subject's YAML example to JSON: `locations{name, description, exits{dir:room}, items[], spawns[]}`, `items{name, description, obtainable}`, `npcs{name, role, dialogue[], stats{hp,damage}, quest?}`, `quests{id, name, type, target, reward}`. Use the RFC's canonical-ID convention (`loc.*`, `item.*`, `npc.*`) since those ids go on the wire in LOOK/TAKE replies. Loader = `os.ReadFile` + `json.Unmarshal`. Done.

**T2.2 — World validator** · *[EXEC after T2.1]*
Walk the loaded world: every exit targets an existing room; every item/NPC/quest reference resolves; quest targets exist; BFS from start proves full connectivity + at least one cycle (subject: loop + branch, full circuit). Fail fast at server startup with a precise message.

**T2.3 — Draft world v1 + quest design** · *Dev B, parallel with Phase 3*
8+ rooms in a loop + branch, 3 NPC roles (dialogue / quest-giver / enemy), 4+ items, 2 quests. Start ugly-but-valid to unblock server testing. Quest design (per RFC §6.1.2, must be documented): per-player `map[questID]state`, states `available → active → completed`; **auto-completion** via hooks on `TAKE` (fetch quest), NPC death (kill quest), `TALK` to giver (delivery + reward into inventory); progress string (`"1/3"`) computed for the QUESTS reply. No quest chains, no COMPLETE_QUEST/ABANDON_QUEST commands — QUEST/QUESTS cover the RFC surface; justify the omission in README.

### Phase 3 — Server core (the backbone)

**T3.1 — TCP skeleton: greeting, per-conn reader/writer, hub** · *Dev A*
`net.Listen("tcp")` → `go handleConn` per accept. **Send `OK hello proto=1` immediately** (RFC §3.2 — server speaks first). Reader: `bufio.Scanner` loop → `protocol.Parse` → dispatch; enforce the AUTHENTICATED state machine (reject gameplay commands before CONNECT). Writer: `for msg := range client.out { ... }`. Hub = mutex-guarded registry with `broadcast(scope, line)` helpers.

**T3.2 — Session: CONNECT, QUIT, WHO, STATS events** · *Dev A*
CONNECT: uniqueness check → `ERR 201 NAME_IN_USE` on duplicate, else `OK connected`, place in start room, ENTER event to room, `EVT STATS players=<n>` to all. QUIT → `OK bye` then the shared cleanup path (also triggered by EOF/error, `defer`red in `handleConn`): remove state → LEAVE event → STATS event. WHO → `OK players=<count>` per RFC (quirk #2 resolved).

**T3.3 — LOOK, MOVE + presence events** · *Dev A*
LOOK: build `protocol.LookReply` from current room, marshal compact, `OK <json>`. MOVE: validate exit → `ERR 301 NO_EXIT`, else swap room under lock, `OK room=<id>`, LEAVE to old room, ENTER to new. Integration with the minimal CLI (T5.1) starts here.

**T3.4 — CHAT + GROUP suite** · *Dev A*
CHAT scope routing on top of the hub; sender receives the EVT too (§2.5). GROUP CREATE/INVITE/JOIN/LEAVE with `401`/`402` per §2.3; one group per player (that's what `402 ALREADY_IN_GROUP` implies); INVITE semantics per your quirk-#4 decision; GROUP events to members.

**T3.5 — Items: TAKE / DROP / INVENTORY** · *Dev A*
Items are **unique instances** (RFC §8): move them between `room.items` and `player.inventory` under the global lock — no duplication possible by construction. Resolve by canonical ID *or* case-insensitive display name, multi-word = match the entire rest-of-line (RFC §8.3/8.4). Replies `OK taken=<id>` / `OK dropped=<id>` with the *canonical id* even when addressed by display name (that's what the RFC examples show).

**T3.6 — TALK + quests (QUEST, QUESTS)** · *Dev A (mechanics per Dev B's T2.3 design)*
TALK → `OK <dialogue line>` (cycle or randomize), `404 NPC_NOT_FOUND` otherwise. QUEST → quest-info JSON or `406 NO_QUEST_AVAILABLE` (also when already completed — RFC is explicit). QUESTS → JSON array with `progress`. Completion validation = T2.3 hooks; state lives in the player struct and dies with the connection (fine — no persistence).

**T3.7 — Combat: ATTACK, STATUS, respawn (+ DEFEND/FLEE)** · *Dev A*
ATTACK → `404` if absent, `405 NPC_NOT_HOSTILE` if not an enemy, else apply damage and reply with the RFC's combat JSON (`attacker_hp`, `target_hp`, `damage`, `status`). Design to document (RFC §6.1.1): turn = one ATTACK; damage = flat weapon/NPC value ± small `math/rand/v2` spread; NPC counterattacks synchronously in the same handler — simplest "turn-based" that satisfies the spec, no scheduler. DEFEND = halve next incoming damage; FLEE = forced MOVE through a random exit with one free counterattack. **Server-side only** — clients never send them by default (interop rule, §2.6). 0 HP → respawn at start room at 50 HP, broadcast to relevant rooms. STATUS → `{"hp","max_hp","status"}` with statuses e.g. `healthy|combat|dead`. <!-- ponytail: synchronous combat; a ticker only if you later want real rounds -->

**T3.8 — Structured logging with `log/slog`** · *[EXEC after T3.1–T3.3]*
`slog.NewJSONHandler(os.Stdout)` as default logger — JSON format, levels, timestamps: three subject checkboxes, zero dependencies. Log connect/disconnect (+IP), every command (+player, args), every reply/error code, world-state changes, quest events. Abuse monitoring (subject + RFC §9.4): sliding counter per connection (e.g. >20 cmds/2 s → `WARN abuse=flood`; rapid reconnects from same IP → `WARN abuse=reconnect`). Counting + logging only, no banning. Fire-and-forget on stdout — can't hurt responsiveness.

### Phase 4 — Hardening (peer-review ammunition)

**T4.1 — Malformed-input gauntlet** · *[EXEC]*
Fire garbage at a live server: unknown verbs, missing args, binary junk and control characters (RFC §9.2 says handle or reject them — decide, document), >1024-byte lines, commands before CONNECT, double CONNECT, `TAKE` ghost items, `GROUP JOIN` nonexistent group, split a command across two TCP writes with a delay (fragmentation test), send five commands in one write (coalescing test). Assert: correct RFC error/reply every time, no crash, no leaked player.

**T4.2 — Disconnect torture** · *Dev A*
Connect 20 clients, chat-spam, `kill -9` half of them mid-broadcast; assert survivors still receive events, `WHO` count and `EVT STATS` are correct. Validates the writer-channel design.

### Phase 5 — CLI client

**T5.1 — Minimal CLI (early, during Phase 3)** · *Dev A*
`net.Dial` + stdin-reader goroutine + socket-reader goroutine printing everything raw. ~60 lines. Exists primarily as the server's dev tool.

**T5.2 — Friendly CLI layer** · *[EXEC after T5.1]*
Subject offers a choice; **choose (2), the translating interface** — reuses `protocol` to parse replies into pretty output (LOOK's JSON → formatted room view; ANSI colors, no lib). `> go north` → `MOVE north`, `> say hi` → `CHAT ROOM hi`, unknown input passed through raw so full RFC syntax still works. Document the choice in README.

### Phase 6 — GUI client (Eliott — start after T1.1 + T3.3 exist)

**T6.1 — Wails backend: connection service** · *Dev B*
`app.go`: `Connect(addr, name)` (handle the `OK hello proto=1` greeting before sending CONNECT), `Send(raw)`, plus typed helpers (`Move(dir)`, `Take(id)`…) using `protocol.Format*`. Reader goroutine: line → `protocol.Parse` → route: replies resolve the pending command, events go straight out via `runtime.EventsEmit(ctx, "tap:evt", payload)`. All protocol logic in Go; test against the real server from day one.

**T6.2 — Frontend layout** · *Dev B*
One screen, CSS grid: room panel (name, description, items, NPCs, exits as clickable direction buttons — all fed by the LOOK payload) · inventory panel (TAKE/DROP buttons per item, accepts display names) · tabbed chat (Global/Room/Group) **separate from** the log pane (subject requirement) · status bar (HP from STATUS, players-in-room from LOOK's `players`, server-wide count live-updated from `EVT STATS`) · action button row (LOOK, MOVE, TAKE, DROP, TALK, ATTACK, STATUS, QUEST, QUESTS, WHO, GROUP, QUIT). Vanilla JS + `EventsOn("tap:evt", render)` is entirely sufficient; skip React/Vue.

**T6.3 — Reactive behaviors** · *Dev B*
Auto re-LOOK after TAKE/DROP/MOVE acknowledgments (subject: room view must reflect item availability); NPC dialogue display on TALK replies; combat JSON rendered into the log pane; quest panel from QUESTS; `ERR` replies as toasts showing code + symbol.

**T6.4 — Interop pass** · *Dev B + [EXEC for fixtures]*
Run the GUI against recorded transcripts of *pure RFC* server output (build fixtures directly from §2's tables), and if possible against another group's server. Test tolerance: extra whitespace, `\r\n` endings, unknown EVT types (ignore, don't crash — other groups may have quirk-#4 interpretations different from yours).

### Phase 7 — World polish

**T7.1 — World v2** · *Dev B*
Coherent theme, good prose, quest flow a stranger can complete in 5 minutes. Run the validator; walk the full circuit manually in the GUI; complete both quests end-to-end.

### Phase 8 — Ship

**T8.1 — README** · *Both, [EXEC first draft from docs/decisions.md]*
Build/run instructions (Makefile targets), architecture summary, and the **justified design choices** both documents demand: combat mechanics (RFC §6.1.1 checklist), quest mechanics (§6.1.2 checklist), CLI interface choice, the four spec-quirk resolutions, data-format choices.

**T8.2 — Defense rehearsal** · *Both*
Fresh-clone build on a clean machine; two-player walkthrough (chat in all three scopes, group invite/join, trade an item, complete both quests, fight, die, respawn); malformed-input + fragmentation demo; log tail showing JSON structure; be ready to whiteboard the mutex/channel model and to point at RFC sections for every wire format you emit.

---

## 7. Suggested calendar & parallelism

```
Week 1: T0.* → T1.* → T2.1/T2.2        (foundations; pair on protocol)
Week 2: Dev A: T3.1–T3.4 + T5.1        Dev B: T2.3 + T6.1        EXEC: T1.2
Week 3: Dev A: T3.5–T3.8               Dev B: T6.2–T6.3          EXEC: T3.8, T4.1
Week 4: Dev A: T4.*, T5.2              Dev B: T6.4, T7.1         EXEC: T5.2, T8.1 draft
Week 5: T8.* buffer + defense prep
```

**EXEC delegation rule:** only tasks with a mechanical spec and an automatic check (tests, validator, gauntlet), always fed the relevant §2 excerpt. Never delegate T1.1 (protocol semantics), T3.1 (concurrency), or any quirk resolution — those errors are silent and expensive.

## 8. Top risks

1. **Spec-quirk mismatch with other groups** (WHO format, GROUP INVITE semantics, CRLF) → resolutions written down in §2, strict emit / tolerant receive, T6.4 transcript testing against pure-RFC fixtures.
2. **JSON payload breaking line framing** → compact marshal only, round-trip tests in T1.2 assert no `\n` in any formatted message.
3. **Concurrency bugs (deadlock, send-on-closed-channel)** → one global mutex, writer channels closed in exactly one place (the cleanup function), `go test -race` in the Makefile.
4. **Go learning curve (Eliott)** → your surface is small: goroutines/channels basics, `net.Conn`, `encoding/json` tags, Wails bindings. Tour of Go's concurrency chapter + reading `protocol/` covers it.
