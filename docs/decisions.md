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

## D10 — Quest design (RFC §6.1.2)

§6.1.2 delegates quest progression, completion, rewards, dependencies and any
extra commands to us, and requires the result to be justified. Below is the
whole system; the two quests it runs are in `data/world.json`.

### State model

Per player, `map[questID]state` over three states — `available → active →
completed`, the vocabulary already fixed in `protocol/payloads.go`. State lives
in the player struct and dies with the connection: no persistence is required by
the subject, so a quest reset on reconnect is a feature of the design, not a
gap. `QUESTS` renders the map; `progress` is `"0/1"` while active and `"1/1"`
once completed, since both our quests have a single objective.

### Acquisition

`QUEST <npc>` is the only entry point. Each quest-giver carries a `quest`
back-pointer in the world data, so the lookup is direct. `406
NO_QUEST_AVAILABLE` covers two cases: the NPC gives no quest, or the player has
already completed it — the RFC explicitly folds "already completed" into this
code. Calling it again while the quest is active is not a third case: D14
returns `OK` with the quest's `active` dialogue line, same as any other
`QUEST` call on a giver whose quest is in progress.

Accepting a quest may hand the player an item, declared as `grants` on the
quest — but `grants` is not the only way an `obtainable: false` item enters
the world: the hunter's `key` arrives as an NPC `drops` entry, and
`water`/`crysknife` arrive as quest `reward`s. The flag is also not enforced
at `TAKE`: an `obtainable: false` item already on the floor (the key, once
dropped) can still be picked up, and must be able to — a hunter killed
before the contract is taken would otherwise leave the boss room
permanently unreachable, the same reasoning the drop-vs-reward choice below
already relies on. `obtainable: false` only means "must never appear in a
location's static `items` list" — enforced by `World.Validate`
(`core/world/validate.go`) at load time — nothing more.

### Completion is automatic, and there are no extra commands

**Decision.** Objectives are validated by hooks on the commands the RFC already
defines. No `COMPLETE_QUEST`, no `ABANDON_QUEST`.

| Type | Hook | Validation | Effect |
|---|---|---|---|
| `deliver` | `TALK <target>` | player's inventory holds `grants` | item consumed, `reward` granted, state → completed |
| `kill` | NPC reaches 0 HP | dying NPC is the quest `target` | `reward` granted, state → completed |

**Rationale.** `QUEST` and `QUESTS` already cover the whole RFC surface, and
§2.6's interoperability rule makes every added command a liability — another
group's client will never send `COMPLETE_QUEST`, so a quest that requires it
would be uncompletable by anyone but us. Hooking the existing commands keeps the
quest system invisible to the protocol: a peer's client completes our quests
without knowing they exist. Manual completion would also need a failure code the
RFC does not define.

### Rewards, and why the key is a drop rather than a reward

Rewards are item instances created into the player's inventory on
completion — `crysknife` for the delivery, `water` for the contract.
Neither exists anywhere else in the *static* world, but "holds by
construction" was single-player reasoning: two players independently
finishing the same quest — or, for a `kill` quest, two holding it active
when the target dies — each triggered their own creation, so the same
canonical id existed twice at once, exactly what `checkItemSources`
rejects for the static data. `Hub.grantOnceLocked` now gates every dynamic
creation (`grants` and `reward` alike) so the id is created once, on
whichever call reaches it first; later completions still update that
player's own quest state, matching the kill quest's shared-credit design
below, they just don't manifest a second copy of an item this world treats
as a singleton.

The hunter's `key` is deliberately **not** the kill quest's reward. It is an NPC
`drops` entry: on death the key falls into the room. If it were the reward, a
player who killed the hunter *before* taking the contract would leave the boss
room permanently unreachable — the quest could never be accepted, the NPC being
dead. As a floor drop it is recoverable by anyone, which is also what §8.2's
item lifecycle describes.

### Quest dialogue lives on the quest, keyed by state

Quest-givers need more than one extra line, so a single `quest_dialogue` field
on the NPC would not have been enough. Each quest carries:

- `offer` — the giver, while the quest is available
- `active` — the giver, while it is in progress (the reminder)
- `complete` — spoken by whoever closes the quest: the **target** for a
  `deliver` quest, the **giver** for a `kill` quest

The NPC's own `dialogue` array is left for `TALK` and is unaffected by quest
state, so a quest-giver reads the same to a player who never takes its quest.

### Gated exits reuse 301 NO_EXIT

A location may carry a `requires` map, `direction → item id`, parallel to its
`exits` map — `door` requires `key` to go north into `boss`. Attempting a gated
exit without the item answers `ERR 301 NO_EXIT`.

**Rationale.** The RFC defines exactly one MOVE failure, and inventing a
`DOOR_LOCKED` code would put a fourth non-RFC symbol on the wire (see D5) for a
condition a peer's client could not render any better than "you can't go that
way". The information the player actually needs belongs in the room prose, not
in an error code, so the `door` description mentions the keyhole. `requires` is
kept as a sibling of `exits` rather than turning exits into objects, so
`exits` stays a plain `direction → room-id` map that copies straight into
`protocol.Room.Exits` for the LOOK payload.

---

## D11 - Canonical ids are applied at load time

The world JSON file keys entities bare ("start", "bone", "guard") and references them bare; the load() function re-keys every map to loc.start / item.bone / npc.guard and rewrites every reference to match : exit targets, requires values, room items, spawns.npc_type, NPC drops, and a quest's giver/target/grants/reward.

**Rationale.** those ids go out on the wire in LOOK, MOVE, TAKE and INVENTORY, so they have to be canonical somewhere. The alternative (bare ids internally, prefixing at the wire boundary) would put a conversion in every handler and a matching strip on every inbound TAKE item.spice. Doing it once at load means data/world.json stays pleasant to hand-edit
---

## D12 — Group id scheme, and two more RFC-silent edge cases

**Gap.** §5.3 defines `GROUP CREATE` → `OK group=<id>` and `GROUP JOIN
<group>` but never says what an `<id>` looks like, nor what happens when
`GROUP INVITE`'s target isn't connected, nor when `GROUP JOIN`'s argument
resolves to nothing (neither a live group id, per quirk #4's fallback, nor a
player currently in one).

**Decision.**

- *Id scheme:* a group's id is its creator's own player name (`GROUP CREATE`
  by `alice` → `OK group=alice`). If that id is already claimed by a
  still-populated group — possible when `alice` left a group that other
  members kept alive, then creates a new one — it is disambiguated with a
  numeric suffix (`alice-2`, `alice-3`, …). A group is deleted the instant
  its last member leaves, so the common case never hits the suffix.
- *`GROUP INVITE <player>` to an unknown or offline name:* replies `OK`
  regardless, same as `CHAT` to an empty room — the invite is fire-and-forget,
  and the RFC defines no error for it.
- *`GROUP JOIN <arg>` resolving to nothing:* a new non-RFC code,
  `404 GROUP_NOT_FOUND` (`protocol.ErrGroupNotFound`), reusing 404 exactly as
  quirk #3 does — disambiguated by symbol, not a fourth meaning for the bare
  code.

**Rationale.** The id scheme needs no coordination or counter: names are
already unique in the hub (`Hub.Register`), so reusing one is free and the
result (`OK group=alice`) is immediately legible to the player who typed
`GROUP CREATE`, unlike an opaque generated id. Silently no-opping the invite
matches how every other scope-with-nobody-listening already behaves in this
codebase (`CHAT ROOM` in an empty room, `CHAT GROUP` with no group — see the
handler). `GROUP_NOT_FOUND` follows D5's reasoning for adding a code at all:
it sits in the RFC's own 4xx/404 range, so a peer applying the §7.3 severity
rule classifies it correctly even without recognising the symbol.

**Where.** `Hub.CreateGroup`, `Hub.JoinGroup`, `Hub.SendTo` in
`core/server/hub.go`; `protocol.ErrGroupNotFound`.

---

## D13 — Presence-style events exclude the actor; CHAT includes them

**Choice.** `ENTER`/`LEAVE` (room and group) are broadcast to everyone
*except* the player who moved, joined, or left. Every `CHAT` scope
(`GLOBAL`/`ROOM`/`GROUP`) is broadcast *including* the sender.

**Rationale.** The two cases aren't parallel. A move/join/leave already gets
a direct reply confirming the action (`OK room=<id>`, `OK group=<id>`, plain
`OK`) — re-announcing it back to the same player as an event would be a
redundant echo of something they already know they just did. `CHAT` has no
such reply carrying the message: per §5.2.1's reply row, the success reply is
a bare `OK`, and the RFC's own example transcript shows the message text
reaching the sender only through the `EVT ... CHAT` broadcast (`D2`'s
transcript). Excluding the sender there would mean their own client never
displays what they just typed.

**Where.** `Hub.BroadcastRoom`/`BroadcastGroup` calls in
`core/server/server.go`: `except: c` for presence, `except: nil` for chat.

---

## D14 — TALK cycles dialogue; QUEST's `description` reuses the quest's own lines

**Decision.** `TALK <npc>` walks `npc.Dialogue` in order and wraps around —
one cursor per NPC, shared by every player who talks to it, not randomized
and not per-player. `QUEST <npc>`'s `description` field is
`quest.Dialogue.Offer` the first time a player calls it for a given quest
(the same call that accepts it — D10 has no separate accept step), and
`quest.Dialogue.Active` on every call after that, while the quest is active.
Once completed, QUEST answers `406` (D10), so `Complete` is never a QUEST
response — it is what `TALK <target>` returns when it closes a deliver
quest.

**Rationale.** Cycling is deterministic and testable — a round-trip test can
assert the exact line, not just membership. A single shared cursor is the
simplest thing that works under the existing global-mutex model and costs
nothing, since dialogue is flavor text, not player state. Reusing the
quest's own offer/active/complete triad for `description` needs no new data
field: `world.Quest` (D10) already carries exactly the three lines QUEST and
TALK between them need to show.

**Where.** `Hub.TalkLine`, `Hub.QuestInfo`, `Hub.CompleteDelivery` in
`core/server/quests.go`.

---

## D15 — Combat design (RFC §6.1.1)

§6.1.1 delegates turn management, damage, combat states and any extra
commands to us. Below is the whole system, ratifying the roadmap's T3.7
proposal.

### No weapons, no regen — HP only moves down, except on respawn

There is no weapon system: `data/world.json`'s items carry no damage field,
so every player hits for the same flat `PlayerBaseDamage` (15), jittered
±20% (`rollDamage`, at least 1). NPCs hit back for their own `stats.damage`,
jittered the same way. `PlayerMaxHP` is 100; there is no HEAL command and no
natural regen, so the only way back to full health is dying and respawning
— and respawn lands at `RespawnHP` (50, per the roadmap), not full. A player
who wins a fight stays wounded until their next death.

### A turn is one ATTACK; the NPC counters synchronously, in the same call

`ATTACK <npc>` resolves entirely inside one lock acquisition
(`Hub.AttackNPC`): c's hit lands, and if the NPC survives it counters
immediately, before the handler returns. There is no scheduler, no combat
session that outlives a single request — the simplest thing that is still
correctly "turn-based". `404 NPC_NOT_FOUND` if npc isn't there (including a
dead one — see below), `405 NPC_NOT_HOSTILE` if its role isn't `enemy`.

### Death: NPCs stay dead, players respawn

An NPC reaching 0 HP is marked dead for good — no respawn, matching a MUD
boss/mini-boss you only fight once. It stops appearing in LOOK, and TALK/
QUEST/ATTACK all answer `404 NPC_NOT_FOUND` for it from then on, exactly as
if it had never been there (`Hub.RoomNPC`/`NPCIn`, replacing the old
world-only `resolveNPC`). Its `drops` fall onto the room's floor, and any
`kill`-type quest targeting it completes for **every player currently
holding it active** — not just whoever landed the blow. Shared credit avoids
inventing a "who gets it in a group" rule for a quest system that otherwise
has none, and it is the only sane option once the NPC is dead for good:
leaving the other holders active would leave them a quest with no target. The
kill also broadcasts `EVT ROOM NPC_DEATH <npc.id>` to everyone else in the
room, excluding the attacker (D13's reasoning: their own `AttackReply`
already carries `target_hp:0`) — without it, other players' only way to learn
the NPC is gone was to LOOK again.

The *reward*, unlike the quest state, is not shared — it cannot be, being a
single item instance. It goes to whoever struck the killing blow, and only
if they had taken the contract themselves. See D20.

A player reaching 0 HP respawns immediately, inside the same `AttackNPC`
call: back to the start room at `RespawnHP`, DEFEND cleared. The handler
then broadcasts the LEAVE/ENTER pair for the old and new rooms, the same
shape `MOVE` already uses.

### A same-tick race: two players finishing off the same NPC

`AttackNPC` re-checks the NPC's HP as its very first statement, still
holding the lock. If it is already 0 — killed by someone else between this
handler's `NPCIn` lookup and this call — the method reports `ok=false` and
the handler answers `404 NPC_NOT_FOUND`, exactly the reply c would have
gotten had it asked one tick later. Without this check the second attacker
would re-run the death branch: drops appended to the room a second time,
breaking §8.1 uniqueness. `TestConcurrentKillDropsOnce` drives exactly that
race, two clients hammering the hunter's last hit point at once.

### STATUS's three values, given there is no combat *session*

`healthy` at max HP, `dead` (0 HP), `combat` otherwise. Because no combat
state outlives a single ATTACK/FLEE call, there is no stored "currently
fighting" flag to report — `combat` here means "wounded", not "engaged right
now". `dead` is only ever seen inline, in the AttackReply of the exchange
that caused it: respawn is synchronous, so a later STATUS call can never
observe 0 HP.

### Gated exits actually check the item now

A gap left over from before TAKE/DROP existed (T3.3's `handleMove` always
answered `301 NO_EXIT` on a gated direction, no matter what the player
carried) is closed as part of this pass: `Hub.Holds` checks the required
item, and only a player without it is turned away. The `door` → `bossroom`
exit — the whole reason the hunter drops a `key` — was unreachable until
this fixed.

**Where.** `core/server/combat.go`; `Hub.Holds` in `core/server/hub.go`;
`handleAttack`/`handleStatus`/`handleMove` in `core/server/server.go`.

---

## D16 — DEFEND and FLEE: non-RFC, server-side only

**Decision.** Two extra commands, on top of the RFC's fixed command table
(§2.3), exist only because §6.1.1 explicitly leaves "extra commands" open
and the roadmap commits to exactly these two: `DEFEND` (no argument →
bare `OK`) arms a one-shot flag that halves the damage of c's *next*
counter-attack, from ATTACK or FLEE, whichever comes first. `FLEE` (no
argument → `OK <FleeReply JSON>`: `room`, `hp`, `damage`, `status`) forces a
move through a random exit c could otherwise walk through normally (a gated
one without the item is never picked), taking one unavoidable hit from any
live enemy in the room on the way out — `damage`/`hp`/`status` report that
hit the same way `AttackReply` reports one from ATTACK, so a fled-from fight
isn't the one combat outcome invisible on the wire; `damage` is `0` and
`status` is c's unchanged status when no enemy shared the room to land one.

Neither has a notion of "which fight" it belongs to, because nothing in this
design does (D15): DEFEND's flag is armed until consumed by whatever hits c
next, even in an unrelated room; FLEE's free hit comes from whatever enemy
happens to share c's current room, not from "the NPC c was just fighting"
specifically. This is the simplest reading that needs no new state beyond a
single bool.

**Rationale — why adding verbs at all is safe.** §2.6's interoperability
rule says extra commands are a liability *if a peer needs them to
function*. Ours don't: a client that never sends DEFEND/FLEE plays a
strictly harder game (every counter-attack lands full, no free escape) but
never breaks a rule the RFC defines. Our own clients only send them by
choice; another group's client will simply never emit `DEFEND`/`FLEE` in
the first place, so their server never has to know these verbs exist. The
two are added as ordinary `protocol.Verb` values (parsed and formatted like
any other), not smuggled through `Command.Arg` — round-trip tests cover them
exactly like RFC verbs, which is what makes them easy to reason about
despite not being in the spec.

**Where.** `protocol.VerbDefend`, `protocol.VerbFlee`, `protocol.FleeReply`;
`Hub.Defend`, `Hub.Flee` in `core/server/combat.go`; `handleDefend`,
`handleFlee` in `core/server/server.go`.

---

## D17 — Structured logging with `log/slog`, and abuse monitoring (§9.4)

**Decision.** `slog.SetDefault` is set once, in `main`, to a
`slog.NewJSONHandler(os.Stdout, nil)` — every package under `core/server`
then just calls the package-level `slog.Info`/`slog.Warn`/`slog.Error`, no
logger threaded through any constructor. That single line buys the subject's
whole logging checklist for zero dependencies: JSON output, leveled records,
and a timestamp on every line, for free from the handler.

### Replies are logged at one choke point, not in every handler

`Client.send` (`hub.go`) is the only function every reply and every
broadcast passes through — 15+ call sites across `server.go`, plus the
`Hub`'s own `Broadcast`/`BroadcastRoom`/`BroadcastGroup`/`SendTo` helpers.
`logReply` hooks there: it reads the line's first token and logs `OK`
(`status=ok`, the rest of the line as `data`) or `ERR` (`status=err`,
`code`, `symbol`); anything else — every `EVT` line — is skipped, since a
broadcast is a notification about someone else's action, not a reply to
*this* client's command, and logging it here would misattribute it. `data`
is capped at `maxLoggedReplyData` (200 bytes, `data_len` added when it
truncates): LOOK's room JSON can run well past that on a room with a full
item/NPC list, and logging it whole on every LOOK buries the commands a
human tailing the log actually cares about under repeated room dumps.

**Rationale.** The alternative was adding an explicit log call to every one
of the ~15 handlers, each producing its own reply. Hooking the single choke
point instead means no handler can forget to log its outcome, at the cost of
`hub.go` — otherwise free of any protocol-wire-format knowledge — sniffing
a line's leading token. That's a real layering blur, accepted deliberately:
the alternative's blast radius (touch every handler, twice, for the rest of
the project) was worse than this one function knowing `"OK"` and `"ERR"`
are the two prefixes that matter.

### What else is logged, and at what level

- **Info** (the default, everything routine): TCP connect/disconnect
  (`+remote`), every parsed command (`+player, verb, scope, sub, arg`),
  every OK/ERR reply (above), world-state changes (MOVE already implied by
  its `room=` reply data; TAKE/DROP explicitly, since nothing logged them
  before), quest events (`quest accepted`, `quest completed`), and combat
  events (`npc killed`, `player respawned`).
- **Warn**: exclusively the two abuse signals below, plus one defensive
  case — a `Verb` reaching the dispatch switch's `default` arm, which
  should be unreachable (`ParseCommand` only ever returns a `Verb` with a
  matching `case`) but would mean a future verb was added to `protocol.go`
  without a handler wired up here.
- **Error**: a JSON marshal failure on an outgoing payload (should never
  happen with these fixed struct shapes; if it does, that is a bug, not a
  player action) and unrecoverable startup failures (bad world file, listen
  failure) — `main.go` logs and `os.Exit(1)`s in place of the old
  `log.Fatalf`, since `slog` has no `Fatal` level of its own.

Ordinary `ERR` replies — walking into a wall, attacking a corpse — are
**not** Warn. They are normal gameplay outcomes a player triggers directly,
already fully captured (`status=err`, `code`, `symbol`) at Info. Warn is
reserved for the two conditions the subject explicitly calls "abuse",
below, so that grepping a log for `"level":"WARN"` means something specific
instead of drowning in routine 404s.

### Abuse monitoring (RFC §9.4, SHOULD): counting and logging only

Two independent, mutex-appropriate trackers, both in `abuse.go`:

- **Flood**: `commandRate`, one per `Client`, holds a sliding window of
  that connection's recent command timestamps. `hit` trims anything older
  than 2 seconds, appends now, and reports true only on the exact tick the
  count reaches 20 — so a sustained flood logs one `WARN abuse=flood`, not
  one per command for as long as it lasts. It lives on the `Client` and is
  touched only by that connection's own reader goroutine (the same one that
  already owns `bufio.Scanner`), so — unlike everything in `hub.go` — it
  needs no lock.
- **Reconnect**: `reconnectTracker`, one per `Server`, keyed by the remote
  address's host (port stripped via `net.SplitHostPort`) with the same
  sliding-window/crossing-tick logic: 3 connections from one host within 10
  seconds logs one `WARN abuse=reconnect`. Unlike flood tracking, this state
  outlives any single `Client` — a reconnect is by definition a *new*
  socket — so it belongs to the `Server` and is mutex-guarded, since every
  connection's goroutine calls `hit` concurrently in `handleConn`.

**Why counting and logging only, never banning.** The subject and RFC §9.4
both frame this as monitoring ("SHOULD" track, nothing stronger), and nothing
in the RFC's command or error tables defines a way to reject a connection
for rate alone — inventing one would mean either silently dropping packets
(§9.3 wants a response to malformed input, and dropping a *connection*
outright is worse) or a wire-visible ban with no RFC error code for it, the
same objection D5 raised for `NOT_CONNECTED`/`BAD_REQUEST`, but for a far
riskier feature: a false positive here (a legitimate burst of `LOOK`s while
sight-reading a new room) would eject a paying — well, playing — customer
mid-session. Counting and logging costs nothing and gives a human everything
they need to act; the roadmap's own thresholds (`>20 cmds/2s`, rapid
reconnects) are exactly what `floodThreshold`/`reconnectThreshold` encode.

**Where.** `core/server/abuse.go`; the `hit` calls in `server.go`'s
`handleConn`; `slog.SetDefault` in `core/cmd/server/main.go`.

---

## D20 — Item uniqueness is enforced by a ledger of what exists

§8.1 makes an item id a *single instance*: at most one of it may exist in
the world at any moment, in one player's pack or on one room's floor. Most
of the code gets this for free — TAKE and DROP move an id between two
containers under one lock, and `World.Validate`'s `checkItemSources` already
refuses a world file where an item could enter play from two places.

Quest grants and rewards are the exception: they are the only paths that
create an item from nothing, and both fire per player. Two players holding
the same deliver quest would each be handed a bone; a kill quest completing
for every holder would hand each of them the reward.

**The rule.** `Hub.spawnedItems` records every item id that currently
exists. `spawnLocked` creates an item into a player's inventory *only* if
its id is absent from that set; `consumeLocked` (delivery) destroys the
instance and frees the id again. Both are the only ways an item enters or
leaves the world after startup, which the tests assert directly: the ledger
must agree, id for id, with a census of every floor and every inventory.

Three consequences worth stating, because they are choices and not
accidents:

- **The kill reward goes to the killer.** `killNPCLocked` closes the quest
  for every holder but calls `spawnLocked` only for the client that landed
  the blow, and only if that client had taken the contract. Granting to
  "every holder" and letting `spawnLocked` drop the duplicates looked
  equivalent but was not: the winner was whichever client the map iteration
  yielded first, which in practice is insertion order — so the earliest
  player to connect reliably collected pay for a kill they never made.
- **An uncontracted kill pays nobody.** If whoever kills the hunter never
  asked the vendor for the contract, the water is never created, and since
  the hunter does not respawn it never can be. Handing it to a bystander
  holder instead would resurrect the bug above. Dropping it on the floor for
  anyone to claim is the obvious alternative if this ever feels too harsh.
- **A consumed grant is re-issued, but a reward is not.** Once the bone is
  delivered its id is free, so the next player to ask the barman — or one
  already stuck holding the quest, who need only ask again — is handed a
  fresh one. The crysknife is *not* freed by anything, so the second player
  to finish the delivery closes the quest unpaid. That asymmetry is what
  unique items mean: the world holds exactly one crysknife, ever.

**Where.** `spawnLocked`/`consumeLocked` in `core/server/hub.go`;
`killNPCLocked` in `core/server/combat.go`; `QuestInfo`/`CompleteDelivery`
in `core/server/quests.go`. `core/server/items_test.go` holds the scenarios,
each asserting the census after every step.
## D19 — CLI interface: the translating layer (T5.2)

**Decision.** The subject offers a choice between a client that speaks only
raw RFC syntax and one that translates a friendlier syntax onto it. We took
the second: `translateInput` maps a handful of natural phrasings onto their
RFC verb — `go north` → `MOVE north`, `say hi` → `CHAT ROOM hi`, `shout`/
`gsay` the same for the other two `CHAT` scopes — and `renderer` turns
replies and events back into readable text (JSON payloads formatted, ANSI
color, no external library). Neither layer can make the RFC syntax stop
working: every trigger word in `translateInput` is one no RFC verb uses, and
its fallback is "return the line unchanged"; `renderer`'s fallback, for
anything it doesn't specifically recognise, is "print the line as it
arrived". `-raw` restores T5.1's original verbatim behavior entirely, for
testing against the wire itself or against another group's server.

**Rationale.** §2.6's interoperability rule is about the *wire*, not the
keys someone types to produce it — a translation layer that degrades
gracefully to raw RFC syntax for anything it doesn't understand costs
nothing there, and reading combat/quest/room JSON as colored prose instead
of a single line is a real usability win for the tool every other roadmap
item gets tested through.

**Pairing replies with the command that caused them.** A reply carries no
verb of its own on the wire — `renderer.expect` records each command's verb
in a small FIFO queue as it's sent, and the next non-`EVT` line pops the
oldest one to know how to render it. This works because both directions of
one TCP connection are strictly ordered: the server processes commands from
one connection's read loop one at a time, replying in the order they
arrived, so a plain queue never needs to correlate by content — T6.1's GUI
backend pairs replies the same way, for the same reason.

**Where.** `core/cmd/cli/translate.go`, `render.go`, `ansi.go`, `main.go`.
## D18 — Malformed-input gauntlet (T4.1): results and control-character policy

**What the gauntlet covers.** Unknown verbs, missing arguments, binary junk
as a verb, commands before CONNECT, double CONNECT, `TAKE`/`GROUP JOIN`
against nonexistent targets, a line over `MaxLineLen`, TCP fragmentation and
coalescing — each is now a regression test in `core/server/gauntlet_test.go`,
run against one real server instance over real TCP, not mocked. All but one
already behaved correctly; that one is below.

### Bug found: a line past `bufio.Scanner`'s own buffer dropped silently

`ParseCommand` has rejected anything over `MaxLineLen` (1024) since D5, but
that check only runs on a line the *scanner* already produced. Scanner's
default token buffer is 64KB — past that, `Scan` returns `false` with
`ErrTooLong`, indistinguishable from the client just closing the connection,
so the read loop exited straight into cleanup with **no reply at all**. That
violates §9.3 ("malformed messages SHOULD result in appropriate error
responses") for a case D5 was never actually exercised against.

**Fix.** `handleConn`'s scanner now takes an explicit buffer just past
`MaxLineLen` (`2×MaxLineLen`, headroom against off-by-one, still a trivial
fixed cost) — any line long enough for `ParseCommand`'s own check to reject
now reaches it and gets a normal `400 BAD_REQUEST`. A line that still
overflows *that* buffer is far enough outside anything a real client would
ever send that giving up on the connection is fine — but `handleConn` now
checks `errors.Is(scanner.Err(), bufio.ErrTooLong)` after the loop and sends
one `400 BAD_REQUEST` first, so even that case is a rejection, not an
unexplained drop. The check is specifically `ErrTooLong`, not a bare
`scanner.Err() != nil`: the first version of this fix treated *any* Scan
failure as a malformed line, so an abrupt disconnect (a real network drop,
or T4.2's `kill -9`-style RST) also tried sending a reply — harmless against
an already-dead connection, but a misleading `BAD_REQUEST` in the logs for
what was never a malformed request. T4.2's disconnect-torture test is what
surfaced this: killing clients mid-broadcast produced exactly that spurious
log line every time.

**Where.** `core/server/server.go`'s `handleConn`.

### Control characters (§9.2: "handle or reject them") — decided per input

§9.2 leaves the choice to us. The two places client input reaches another
client's screen raw (not JSON, which already escapes this) don't share the
same risk:

- **A raw `\n`/`\r` forging an extra wire line — impossible for client
  input.** The transport is itself line-delimited on `\n`, so nothing a
  client sends within one command can *contain* a raw newline; that risk is
  unique to data loaded from a file (D10's `hasControlChar` in
  `world/validate.go`), not to anything arriving over the socket.
- **A control character riding along in an echoed value is still possible,
  and usernames are the one place it matters.** `CONNECT`'s argument is
  echoed raw in every `PRESENCE`/`GROUP`/`CHAT` event for the rest of that
  session — a terminal escape sequence in it lands in every other player's
  raw-printing T5.1 CLI, repeatedly, for as long as the name is in use.
  **Decision: reject.** `ParseCommand`'s `CONNECT` case now also rejects a
  control character in the username, the same 400 it already gives an empty
  or space-containing one (`protocol.hasControlChar`, mirroring `world`'s
  but kept separate — `protocol` importing `world` for one helper would be
  a real layering violation for no shared state).
- **`CHAT`'s message is the other raw-echoed value, and it's accepted
  as-is.** It's genuinely free text by RFC design (§5.2.1's example puts no
  restriction on it), one-shot rather than persistent like a username, and
  rejecting bytes we can't fully anticipate risks breaking a peer's client
  sending something legitimate we didn't think of. A hostile escape
  sequence here is a rendering concern for whichever client chooses to
  print it raw — T5.2's translating CLI layer is the right place to sanitize
  on display, not the server.

### Everything else, confirmed correct as-is

Double `CONNECT` on an already-authenticated connection reuses `201
NAME_IN_USE` — no RFC code covers "you're already connected", and this was
already the behavior (`handleConnect`'s `c.name != ""` guard existed before
T4.1); the gauntlet just confirms it also leaves the *first* identity's
state untouched. Fragmentation and coalescing both fall out of
`bufio.Scanner` for free, exactly as D1's rationale expected — the gauntlet
proves it rather than assuming it. `TAKE`/`GROUP JOIN` against ids that
don't exist already answered `404`/`404 GROUP_NOT_FOUND` correctly (D12).

**Where.** `core/server/gauntlet_test.go`; `protocol.hasControlChar`,
`ParseCommand`'s `VerbConnect` case.

---

## D21 — An enemy can name an item that its attacker needs

The boss is meant to be the crysknife's reason to exist: the bone quest pays
in a blade, and the blade is what makes the last fight winnable. Encoding
that as "`ATTACK npc.boss` is refused without `item.crysknife`" would have
been a lie about the world — the boss is standing right there — so the rule
is expressed as damage instead.

**The rule.** An NPC may carry `"requires": "<item>"` in `data/world.json`.
A player attacking it without that item in inventory deals `UnarmedDamage`
(1) instead of a `rollDamage(PlayerBaseDamage)` roll, flat and unjittered —
the point is that the number is derisory every single time, which reads as
"your blows barely scratch it" rather than as bad luck. Everything else about
the exchange is unchanged: the enemy counters at full strength, DEFEND and
FLEE work, death and respawn work. Carrying the item restores ordinary
combat, and dropping it takes it away again — the check is on the inventory
at the moment of the blow, not on a flag set when the fight began.

**Why in the world file rather than in `combat.go`.** Hardcoding
`npc.boss`/`item.crysknife` in the server would put content in code, which
D11's canonical-id convention and `Validate`'s reference checking exist to
prevent. As data it costs one field, and `Validate` earns its keep: it
refuses a world naming an item that does not exist, and refuses to hang the
rule on a non-enemy, which could never be attacked and so would silently do
nothing. It also reuses the vocabulary gated exits already use
(`Location.Requires`), for the same idea — this needs that item.

**Known: 1 damage is not the same as unbeatable.** Enemy HP never
regenerates (D15) and death costs only a walk back from the start room with
your inventory intact, so a determined player can grind the boss down
bare-handed. Measured, not guessed: 60 hits across 10 deaths. Closing that
would mean either 0 damage — which turns the fight back into a refusal, the
thing this design avoids — or healing an enemy once no one is fighting it,
which is a new mechanic and a larger decision than this one.

**Where.** `NPC.Requires` in `core/world/world.go` (canonicalised at load
like every other reference); its checks in `core/world/validate.go`;
`UnarmedDamage` and the `AttackNPC` branch in `core/server/combat.go`;
`core/server/boss_test.go`.

---

## Still open

- **Control characters in messages** (§9.2: "reject or safely handle") — decide
  during T4.1's malformed-input gauntlet.
- **CLI interface** — subject offers "raw RFC syntax" vs "translating layer";
  roadmap T5.2 picks the translating layer, to be confirmed once the CLI exists.
