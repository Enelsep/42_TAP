package server

import (
	"math/rand/v2"
	"strings"

	"github.com/Enelsep/42_TAP/core/protocol"
	"github.com/Enelsep/42_TAP/core/world"
)

// Combat constants (D15, RFC §6.1.1). There is no weapon system and no
// natural regen: PlayerBaseDamage is flat for every player, and the only way
// back to full HP is death.
const (
	PlayerMaxHP      = 100
	RespawnHP        = 50 // roadmap T3.7: 0 HP respawns at the start room, at half health
	PlayerBaseDamage = 15

	// UnarmedDamage is what a hit lands for against an enemy whose world
	// data names an item the attacker isn't carrying (D21) — a scratch, not
	// a refusal, so the fight still plays out as a fight.
	UnarmedDamage = 1
)

// rollDamage jitters base by roughly ±20% (at least ±1) and never returns
// less than 1 — an attack always does *something*, so combat can never stall
// on a zero-damage roll.
func rollDamage(base int) int {
	spread := max(1, base/5)
	dmg := base - spread + rand.IntN(2*spread+1)
	return max(1, dmg)
}

// roomNPCLocked is RoomNPC's body, for callers that already hold h.mu.
func (h *Hub) roomNPCLocked(room string) *world.NPC {
	npc := npcAt(h.world, room)
	if npc == nil {
		return nil
	}
	if npc.Role == world.RoleEnemy && h.npcHP[npc.ID] <= 0 {
		return nil // dead enemies are gone for good — no respawn (D15)
	}
	return npc
}

// RoomNPC returns the NPC standing in room, or nil if there is none, or the
// one that was there has been killed.
func (h *Hub) RoomNPC(room string) *world.NPC {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.roomNPCLocked(room)
}

// NPCIn resolves arg against RoomNPC, by canonical id or case-insensitive
// display name — the same resolution rule TAKE/DROP use for items (RFC
// §8.3/8.4). TALK, QUEST and ATTACK all go through this, never npcAt
// directly, so a dead enemy reads as "not here" everywhere alike.
func (h *Hub) NPCIn(room, arg string) *world.NPC {
	npc := h.RoomNPC(room)
	if npc == nil || (npc.ID != arg && !strings.EqualFold(npc.Name, arg)) {
		return nil
	}
	return npc
}

// respawnLocked sends c back to the start room at RespawnHP, clears any
// armed DEFEND, and clears the caller's obligation to keep going — the whole
// of what "you died" means in this design (h.mu must already be held).
func (h *Hub) respawnLocked(c *Client) {
	h.setRoomLocked(c, h.world.Start)
	c.hp = RespawnHP
	c.defending = false
}

// killNPCLocked marks npc dead for good, drops its items into room, and
// completes the kill quest (if any) for every holder, not just the killer
// (D15). The reward stays unshared — a single item instance (RFC §8) — so
// only killer gets it, and only if killer held the contract. h.mu must
// already be held.
func (h *Hub) killNPCLocked(killer *Client, room string, npc *world.NPC) {
	h.npcHP[npc.ID] = 0
	for _, drop := range npc.Drops {
		h.roomItems[room] = append(h.roomItems[room], drop)
		h.spawnedItems[drop] = true
	}
	for _, q := range h.world.Quests {
		if q.Type != world.QuestKill || q.Target != npc.ID {
			continue
		}
		earned := killer.quests[q.ID] == protocol.QuestActive
		for _, client := range h.clients {
			if client.quests[q.ID] == protocol.QuestActive {
				client.quests[q.ID] = protocol.QuestCompleted
			}
		}
		if earned {
			h.spawnLocked(killer, q.Reward)
		}
	}
}

// AttackNPC resolves one ATTACK turn: c's hit lands first, npc counters
// synchronously if it survives — this server's whole "turn-based" combat,
// no scheduler (D15). ok is false if npc already died before this call; the
// caller should treat that exactly like NPC_NOT_FOUND.
func (h *Hub) AttackNPC(c *Client, npc *world.NPC) (reply protocol.AttackReply, npcDied, respawned, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.npcHP[npc.ID] <= 0 {
		return protocol.AttackReply{}, false, false, false
	}

	dmg := rollDamage(PlayerBaseDamage)
	if npc.Requires != "" && !c.inventory[npc.Requires] {
		dmg = UnarmedDamage // no jitter: the point is that it is derisory, every time
	}
	hp := max(0, h.npcHP[npc.ID]-dmg)
	h.npcHP[npc.ID] = hp

	if hp == 0 {
		h.killNPCLocked(c, c.room, npc)
		// c never took a counter on this branch, but c.hp may already be
		// below max from an earlier fight — killing the NPC doesn't heal it.
		return protocol.AttackReply{AttackerHP: c.hp, TargetHP: 0, Damage: dmg, Status: statusFor(c.hp)}, true, false, true
	}

	counter := rollDamage(npc.Stats.Damage)
	if c.defending {
		counter -= counter / 2
		c.defending = false
	}
	c.hp = max(0, c.hp-counter)

	if c.hp == 0 {
		h.respawnLocked(c) // sets c.hp to RespawnHP — capture 0 first, or the reply lies about the HP that caused "dead"
		return protocol.AttackReply{AttackerHP: 0, TargetHP: hp, Damage: dmg, Status: protocol.StatusDead}, false, true, true
	}
	return protocol.AttackReply{AttackerHP: c.hp, TargetHP: hp, Damage: dmg, Status: protocol.StatusCombat}, false, false, true
}

// statusFor derives the Status* string for an HP value (D15): "healthy" at
// max HP, "dead" at 0, "combat" otherwise. Shared by StatusOf and Flee so
// there is exactly one place that encodes this three-way split.
func statusFor(hp int) string {
	switch {
	case hp == 0:
		return protocol.StatusDead
	case hp < PlayerMaxHP:
		return protocol.StatusCombat
	default:
		return protocol.StatusHealthy
	}
}

// StatusOf reports c's current HP and derived status (D15). No "currently
// fighting" state exists to track — a turn never outlives one ATTACK/FLEE.
func (h *Hub) StatusOf(c *Client) protocol.StatusReply {
	h.mu.Lock()
	defer h.mu.Unlock()
	return protocol.StatusReply{HP: c.hp, MaxHP: PlayerMaxHP, Status: statusFor(c.hp)}
}

// Defend arms a one-shot flag halving c's next counter-attack, from ATTACK
// or FLEE, whichever comes first (D15/D16).
func (h *Hub) Defend(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c.defending = true
}

// Flee moves c through a random usable exit (never a gated one), taking one
// free hit from any live enemy in the room on the way out. respawned
// reports whether that hit was fatal, in which case c ends up at the
// world's start room instead of the fled-to one. ok is false only if the
// room has no usable exit.
func (h *Hub) Flee(c *Client) (reply protocol.FleeReply, respawned, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	loc := h.world.Locations[c.room]
	var exits []string
	for dir, target := range loc.Exits {
		if _, gated := loc.Requires[dir]; gated {
			continue
		}
		exits = append(exits, target)
	}
	if len(exits) == 0 {
		return protocol.FleeReply{}, false, false
	}
	target := exits[rand.IntN(len(exits))]

	var dmg int
	if npc := h.roomNPCLocked(c.room); npc != nil && npc.Role == world.RoleEnemy {
		dmg = rollDamage(npc.Stats.Damage)
		if c.defending {
			dmg -= dmg / 2
			c.defending = false
		}
		c.hp = max(0, c.hp-dmg)
		if c.hp == 0 {
			h.respawnLocked(c) // sets c.hp to RespawnHP — capture 0 first, same reasoning as AttackNPC
			return protocol.FleeReply{Room: h.world.Start, HP: 0, Damage: dmg, Status: protocol.StatusDead}, true, true
		}
	}
	h.setRoomLocked(c, target)
	return protocol.FleeReply{Room: target, HP: c.hp, Damage: dmg, Status: statusFor(c.hp)}, false, true
}
