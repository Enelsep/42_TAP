package server

import (
	"slices"
	"strings"

	"github.com/Enelsep/42_TAP/core/protocol"
	"github.com/Enelsep/42_TAP/core/world"
)

// npcAt returns the NPC spawned in room, or nil if there is none — the raw
// world-data lookup, with no notion of whether an enemy has since been
// killed. RoomNPC (combat.go) layers that liveness check on top; TALK/QUEST
// resolve NPCs through Hub.NPCIn, never through this function directly.
func npcAt(w *world.World, room string) *world.NPC {
	loc := w.Locations[room]
	if loc == nil || loc.Spawns == nil {
		return nil
	}
	return w.NPCs[loc.Spawns.NPCType]
}

// TalkLine returns npc's next dialogue line, cycling through world.NPC.Dialogue
// and wrapping around. The cursor is per-NPC and shared by every player who
// talks to it (D14) — flavor text, not player state, so one cursor is enough.
func (h *Hub) TalkLine(npc *world.NPC) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := h.npcTalk[npc.ID] % len(npc.Dialogue)
	h.npcTalk[npc.ID]++
	return npc.Dialogue[i]
}

// CompleteDelivery checks whether npc is the target of a deliver quest c is
// carrying the item for, and if so closes it: item consumed, reward
// granted, state completed (D10). line is the quest's Complete dialogue,
// meant to replace npc's own TalkLine, not combine with it.
func (h *Hub) CompleteDelivery(c *Client, npc *world.NPC) (line, quest string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, q := range h.world.Quests {
		if q.Type != world.QuestDeliver || q.Target != npc.ID {
			continue
		}
		if c.quests[q.ID] != protocol.QuestActive || !c.inventory[q.Grants] {
			continue
		}
		h.consumeLocked(c, q.Grants)
		h.spawnLocked(c, q.Reward)
		c.quests[q.ID] = protocol.QuestCompleted
		return q.Dialogue.Complete, q.ID, true
	}
	return "", "", false
}

// QuestInfo resolves QUEST <npc>. The first call accepts the quest on the
// spot — D10 has no separate accept step — and grants q.Grants straight
// into the inventory. ok is false if npc offers no quest, or it's already
// completed (406, D10).
func (h *Hub) QuestInfo(c *Client, npc *world.NPC) (q *world.Quest, description, status string, justAccepted, ok bool) {
	if npc.Quest == "" {
		return nil, "", "", false, false
	}
	q = h.world.Quests[npc.Quest]

	h.mu.Lock()
	defer h.mu.Unlock()
	switch c.quests[q.ID] {
	case protocol.QuestCompleted:
		return nil, "", "", false, false
	case protocol.QuestActive:
		// Re-hands the grant if the instance is gone and c isn't holding it;
		// spawnLocked no-ops otherwise, so this can't mint a second one.
		h.spawnLocked(c, q.Grants)
		return q, q.Dialogue.Active, protocol.QuestActive, false, true
	default:
		c.quests[q.ID] = protocol.QuestActive
		h.spawnLocked(c, q.Grants)
		return q, q.Dialogue.Offer, protocol.QuestActive, true, true
	}
}

// QuestsFor returns c's known quests — active or completed, sorted by id.
// D10: progress is "0/1" while active and "1/1" once completed, since both
// our quests have a single objective.
func (h *Hub) QuestsFor(c *Client) []protocol.QuestEntry {
	h.mu.Lock()
	defer h.mu.Unlock()

	entries := make([]protocol.QuestEntry, 0, len(c.quests))
	for id, status := range c.quests {
		progress := "0/1"
		if status == protocol.QuestCompleted {
			progress = "1/1"
		}
		entries = append(entries, protocol.QuestEntry{QuestID: id, Status: status, Progress: progress})
	}
	slices.SortFunc(entries, func(a, b protocol.QuestEntry) int {
		return strings.Compare(a.QuestID, b.QuestID)
	})
	return entries
}
