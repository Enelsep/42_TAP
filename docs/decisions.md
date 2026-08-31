# Design decisions

Running log of every choice RFC 42TAP leaves to us, or forces on us where the
spec contradicts itself.

---

## D1 — Line terminator: emit LF, tolerate CRLF (quirk #1)

**Conflict.** §2.1 and §4.1 terminate every message with LF (`0x0A`). The
CONNECT ABNF of §3.1 says `CRLF`.

**Decision.** Always emit `LF`. On receive, strip a single trailing `CR` from
every line before parsing.

**Rationale.** LF is stated twice, in the normative framing sections, against
one occurrence in a single command's grammar — the RFC's own §2.1
("Message Delimiter: Line Feed") is the general rule, so the `CRLF` is best read
as an editing slip. Stripping `CR` on receive costs one line of code and makes
us interoperable with any group that read §3.1 literally, or with a peer testing
us through `telnet`, which sends CRLF.

**Where.** `protocol.LineTerm`; the `CR` strip lives in `Parse`.

---

## D2 — WHO returns `players=<count>` (quirk #2)

**Conflict.** The RFC (§5.2.2) specifies `OK players=<count>`. The subject PDF's
example instead shows `OK { "room": ["alice","bob"], "server": 5 }`.

**Decision.** Follow the RFC: `OK players=<count>`, the server-wide count of
connected players.

**Rationale.** The RFC is the normative document and, more importantly, it is
the interoperability contract — our clients must work against other groups'
servers and vice versa, and every group is handed the same RFC. A JSON object
where a peer expects `players=` breaks that contract; the reverse does not,
since a client can derive everything the PDF's shape carried from data it
already has.

**Consequence.** The GUI's "players in this room" counter is computed from the
`players` array of the LOOK payload, not from WHO. The server-wide counter comes
from WHO once at connect and is then kept live by `EVT STATS players=<n>`.

---

## D3 — Error code 404 is disambiguated by symbol, never by code (quirk #3)

**Conflict.** §8.2 assigns code `404` to three distinct conditions:
`ITEM_NOT_FOUND`, `ITEM_NOT_IN_INVENTORY` and `NPC_NOT_FOUND`.

**Decision.** `protocol.Error` carries both `Code` and `Symbol`, and all
dispatch — ours and, we assume, our peers' — keys on `Symbol`. `Code` is used
only for the 4xx/9xx severity split of §7.3, exposed as `Error.Fatal()`.

**Rationale.** The code alone cannot distinguish "that item isn't here" from
"you aren't carrying that" from "there's no such NPC" — three errors that need
three different messages in the UI. The symbol is the only unambiguous part of
the pair, so it is the part we treat as significant.

**Where.** `protocol.Error`, the `Sym*` constants, and the `Err*` table.

---

## D4 — GROUP INVITE: the event carries the inviter (quirk #4)

**Conflict.** §6.2.3 defines the event as `EVT GROUP INVITE <player>` with no
group identifier, so an invitee has nothing to pass to `GROUP JOIN <group>`.

**Decision.** Two halves:

- *Emit:* `EVT GROUP INVITE <inviter>` — the `<player>` slot holds the name of
  the player who sent the invitation.
- *Accept:* `GROUP JOIN <arg>` resolves `<arg>` as a group id first, and falls
  back to "the group that this player belongs to". So a peer that puts the group
  id in the event still works against our server, and our own clients can join
  by echoing back the inviter's name.

**Rationale.** The event's single slot has to carry whatever the invitee needs
to act, and the RFC's `event-data` is free-form. Naming the inviter is the more
useful of the two readings (the UI wants to show *who* invited you), and the
JOIN fallback makes the choice cost nothing in interoperability: both
interpretations of the event resolve correctly on our side.

**Client-side tolerance.** Our clients ignore unknown trailing tokens on any
event line and never drop the connection on an unrecognised event type — other
groups may have resolved this differently, or added fields.

---

## D5 — Two non-RFC error codes: 202 NOT_CONNECTED and 400 BAD_REQUEST

**Gap.** §8.2's table has no entry for two conditions the RFC nevertheless
requires us to handle: a gameplay command arriving in state `CONNECTED` before a
successful CONNECT (§2.2's state machine implies it is invalid, §3.3 says
nothing), and a malformed line (§9.3: "Malformed messages SHOULD result in
appropriate error responses" — without saying which).

**Decision.** Add exactly two codes, chosen to sit in the RFC's existing ranges:

| Code | Symbol | Condition |
|---|---|---|
| `202` | `NOT_CONNECTED` | any command other than CONNECT/QUIT received before authentication |
| `400` | `BAD_REQUEST` | unknown verb, missing or invalid argument, unknown CHAT scope or GROUP subcommand, line over 1024 bytes |

**Rationale.** 2xx is the RFC's session/authentication range (`201
NAME_IN_USE`), 4xx its recoverable-client-error range; both new codes are
therefore classified correctly by any peer applying the §7.3 severity rule, even
one that has never seen the symbol. Silence was the alternative — dropping the
line, or the connection — and it is worse: §9.3 asks for a response, and a peer
debugging against our server learns nothing from a hang.

**Interoperability.** These are receive-side additions only: our clients never
*send* anything that depends on them, and an unknown symbol arriving from
another group's server is displayed, not acted upon. The 4xx/9xx split means a
peer treats `400` as recoverable and keeps the session alive, which is the
correct behaviour.

**Where.** `protocol.ErrNotConnected`, `protocol.ErrBadRequest`, marked as
non-RFC in the source.

---

## D6 — Command modelling: one rest-of-line argument

**Decision.** `protocol.Command` is `{Verb, Scope, Sub, Arg}` — a single
free-form `Arg` holding everything after the verb and its optional keyword,
never tokenised further.

**Rationale.** The §4.1 grammar is `command-name [SP arguments]`, and the only
commands with more structure are CHAT (one scope keyword) and GROUP (one
subcommand keyword). Splitting `Arg` on spaces would break the two cases the RFC
explicitly requires: multi-word chat messages (§5.2.1) and multi-word resource
names (§8.4) — `TAKE Rusty Sword` must reach the item resolver whole.

---

## D7 — JSON payloads: compact, and never `null` for a collection

**Decision.** Payloads are marshalled with `json.Marshal` (never
`MarshalIndent`), and every slice field is non-nil before marshalling, so an
empty collection is `[]`.

**Rationale.** A raw newline inside a payload would split one message into two
and desynchronise the peer's line framing for the rest of the session — the
single most damaging bug available to us here. `json.Marshal` emits no newlines
and escapes any inside strings, so compliance is free as long as we never pretty
print. Separately, the RFC's examples show `"items":[]`; a nil Go slice would
emit `null` and force every peer to special-case it. Both are asserted by the
round-trip tests in `protocol_test.go`.

---

## D8 — Parser tolerance policy

**Decision.** One rule governs every `Parse*` function: **unknown constructs are
accepted, malformed known constructs are rejected.**

- Accepted: any case of verbs, keywords and `OK`/`ERR`; repeated or surrounding
  whitespace; a trailing `CR`; trailing tokens on a command that takes no
  argument or on an event we already understand; event scopes and kinds we have
  never heard of (returned with `Raw` set, for the client to log or ignore).
- Rejected with `400 BAD_REQUEST`: empty line, unknown verb, missing required
  argument, unknown CHAT scope, unknown or missing GROUP subcommand, a line over
  1024 bytes, and an `EVT STATS` whose count is not a number.

**Rationale.** The two halves are not in tension: tolerating what we do not
understand is what keeps us working against another group's implementation,
while rejecting what we *do* understand but cannot use is what stops a silently
wrong value — a player count of zero, an item nobody named — from propagating
into game state.

**Corollary — usernames are a single token.** `CONNECT alice smith` is rejected.
A name with a space would make `EVT ROOM CHAT <player> <message…>` ambiguous for
every client on the server, ours included, since nothing marks where the name
ends. §4.1's `arguments = 1*VCHAR` (VCHAR excludes space) supports reading this
as the RFC's intent.

**Line limit on receive only for commands.** §9.4's 1024-byte recommendation is
enforced in `ParseCommand`, not in `ParseReply`/`ParseEvent`: a LOOK payload
from a large room can legitimately be longer, and refusing to read a peer's
reply would be exactly the intolerance D1 and D4 set out to avoid.

---

## D9 — Format\* emits the line terminator

**Decision.** Every `Format*` function returns a string that already ends with
`LineTerm`; callers write it to the socket unchanged.

**Rationale.** Framing is the protocol layer's responsibility. If terminators
were the caller's job, a single forgotten `"\n"` in a broadcast path would
silently glue two messages together and desynchronise a peer for the rest of the
session — the same class of failure as D7, and just as hard to spot in a log.
Making it impossible to express costs nothing; `Parse*` accepts lines with or
without a terminator, so round-tripping still works.

---

## Still open

- **Combat** (§6.1.1) — turn management, damage formula, DEFEND/FLEE, respawn
  rules. Proposal in roadmap T3.7, to be ratified when the server's combat path
  is written.
- **Quests** (§6.1.2) — progression tracking, auto vs manual completion, reward
  delivery. Proposal in roadmap T2.3; the `available → active → completed`
  vocabulary is already fixed in `protocol/payloads.go`.
- **CLI interface** — subject offers "raw RFC syntax" vs "translating layer";
  roadmap T5.2 picks the translating layer, to be confirmed once the CLI exists.
- **Control characters in messages** (§9.2: "reject or safely handle") — decide
  during T4.1's malformed-input gauntlet.
