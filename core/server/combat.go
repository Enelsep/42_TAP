package server

import (
	"math/rand/v2"
	"strings"

	"github.com/Enelsep/42_TAP/core/protocol"
	"github.com/Enelsep/42_TAP/core/world"
)

const (
	PlayerMaxHP      = 100
	RespawnHP        = 50
	PlayerBaseDamage = 15
	UnarmedDamage    = 1
)

func rollDamage(base int) int {
	spread := max(1, base/5)
	dmg := base - spread + rand.IntN(2*spread+1)
	return max(1, dmg)
}

func (h *Hub) roomNPCLocked(room string) *world.NPC {
	npc := npcAt(h.world, room)
	if npc == nil {
		return nil
	}
	if npc.Role == world.RoleEnemy && h.npcHP[npc.ID] <= 0 {
		return nil
	}
	return npc
}

func (h *Hub) RoomNPC(room string) *world.NPC {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.roomNPCLocked(room)
}

func (h *Hub) NPCIn(room, arg string) *world.NPC {
	npc := h.RoomNPC(room)
	if npc == nil || (npc.ID != arg && !strings.EqualFold(npc.Name, arg)) {
		return nil
	}
	return npc
}

func (h *Hub) respawnLocked(c *Client) {
	h.setRoomLocked(c, h.world.Start)
	c.hp = RespawnHP
	c.defending = false
}

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
			h.respawnLocked(c)
			return protocol.FleeReply{Room: h.world.Start, HP: 0, Damage: dmg, Status: protocol.StatusDead}, true, true
		}
	}
	h.setRoomLocked(c, target)
	return protocol.FleeReply{Room: target, HP: c.hp, Damage: dmg, Status: statusFor(c.hp)}, false, true
}
