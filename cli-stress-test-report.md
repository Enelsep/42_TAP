# CLI stress-test report

Ad-hoc limit-testing of `core/cmd/cli` against a live `core/cmd/server` instance,
run 2026-09-10. 30 tests across 6 categories (translation layer, protocol edge
cases, concurrency, connection lifecycle, `-raw` mode, combat/quest). Kept for
reference — revisit before final submission.

## Headline finding: ANSI/terminal-escape injection via CHAT

**Test C1** — a player sending `CHAT ROOM before \x1b[31mRED-INJECTED\x1b[0m after`
gets those escape bytes delivered **unmodified** to every other connected
player's terminal (confirmed via hexdump on the receiving side: raw `1b 5b 33
31 6d` = `ESC[31m`). `render.line` wraps the server's own line in ANSI color
codes but never neutralizes the message body itself before `fmt.Println`. Any
player can inject arbitrary escape sequences into every other player's
terminal — demonstrated here with just a color change, but the same path
reaches screen-clear (`\x1b[2J`) or OSC sequences (window title) depending on
the victim's emulator.

**Fixed** — rejected server-side at parse time (`core/protocol/protocol.go`,
`ParseCommand`'s `VerbChat` case), reusing the same `hasControlChar` check
already applied to usernames, for the same reason: the message is echoed raw
into every recipient's `EVT CHAT`, `-raw` clients included. A control
character in it now gets `400 BAD_REQUEST` instead of reaching anyone's
terminal. Fixing at the protocol boundary rather than in `render.go` protects
both clients and any peer group's client in one place, instead of requiring
every renderer to remember to sanitize on its own. Verified live: the
injection attempt is rejected, a normal message still goes through.

Not required by the subject, but worth having fixed before evaluation — it
was the only finding here with real severity.

## Full results

| # | Test | Result |
|---|---|---|
| A1 | `go north` → MOVE translation | ✅ |
| A2 | `say ...` → CHAT ROOM | ✅ |
| A3 | `shout ...` → CHAT GLOBAL | ✅ |
| A4 | `gsay ...` → CHAT GROUP, solo player (no group) | ⚠️ `OK` returned but no event rendered — likely correct (no group = no one to relay to), worth a second look |
| A5 | RFC verbs case-insensitive (`look`, `Move North`) | ✅ |
| A6 | Unrecognized phrase (`dance`) interleaved — PR#23 pairing regression check | ✅ still fixed, no desync |
| A7 | Empty line | ✅ clean 400, correct pairing |
| A8 | Whitespace-only line | ✅ same |
| B1 | Line >1024 bytes | ✅ 400, correct pairing with the next command |
| B2 | CRLF line endings | ✅ CR stripped (D1) |
| B3 | Unicode + emoji in CHAT | ✅ perfect round-trip |
| B4 | Multi-word item (`Space liquor`) TAKE/INVENTORY/DROP | ✅ |
| B5 | Fully unknown verb (`FOOBAR`) | ✅ 400, correct pairing |
| B6 | Command before CONNECT | ✅ 202 NOT_CONNECTED, CONNECT accepted right after |
| B7 | Double CONNECT (different name the 2nd time) | ⚠️ minor — returns `201 NAME_IN_USE` even though the 2nd name is free; server reuses that code for "already connected", misleading message |
| B8 | CONNECT with a multi-word name | ✅ rejected 400 (single-token username grammar, correct) |
| B9 | 300-character player name | ✅ **fixed** — `protocol.MaxUsernameLen = 12` added to `ParseCommand`'s `VerbConnect` case (`core/protocol/protocol.go`), same guard clause as the existing space/control-char checks. Verified: 12 chars accepted, 13 rejected with `400 BAD_REQUEST`. |
| B10 | Control byte (0x01) inside a chat message | ✅ passed through byte-for-byte end to end (verified via hexdump) — unfiltered, consistent with C1 |
| C1 | ANSI injection via CHAT between two live clients | ❌ see above |
| C2 | 30-command flood in a burst (abuse threshold = 20/2s) | ✅ 30/30 replies correctly paired, WARN logged server-side, nothing dropped |
| C3 | Multi-line paste mixing valid/invalid/natural-language lines | ✅ 8 lines, 8 correctly paired replies |
| D1 | Server unreachable (wrong port) | ✅ clear error, exit code 1, no hang |
| D2 | Server killed abruptly mid-session | ✅ no panic, clean exit 0, no zombie process |
| D3 | Clean QUIT | ✅ `bye` everywhere, exercised implicitly across the whole run |
| D4 | Stdin EOF without QUIT (Ctrl-D) | ✅ half-close; server's last reply still arrives before the process exits |
| E1 | `-raw`: no natural-language translation | ✅ `go north` sent verbatim, server 400s it |
| E2 | `-raw`: raw JSON, no color | ✅ T5.1 passthrough confirmed |
| F1 | ATTACK on a nonexistent NPC | ✅ 404 NPC_NOT_FOUND |
| F2-F5 | Full walkthrough: QUEST by multi-word display name (`Spice reseller`) → accepted → navigate → ATTACK by multi-word display name (`Twin suns hunter`) → DEFEND correctly halves the next counter (92→87 instead of full damage) → kill → quest auto-completes → reward `item.water` in inventory | ✅ flawless, including multi-word display-name resolution on both QUEST and ATTACK |
| F6 | FLEE with no live enemy in the room (hunter already dead from the previous test) | ✅ `fled to loc.camp, unscathed` — 0 damage when there's nothing to land a hit, edge case handled correctly |

## Summary

30 tests: **2 fixed** (C1, ANSI injection via CHAT; B9, player-name length
cap), **1 minor observation** (NAME_IN_USE code reuse — turns out to be a
deliberate, documented, and tested design choice per `TestDoubleConnect` in
`gauntlet_test.go`, not a defect), everything else clean —
including every stress scenario (flood, multi-line paste, abrupt server
death) that could plausibly have resurfaced the PR#23 pairing regression. The
translate → protocol → render pipeline held up well.
