package server

import (
	"slices"
	"strings"

	"github.com/Enelsep/42_TAP/core/protocol"
	"github.com/Enelsep/42_TAP/core/world"
)

// resolveNPC finds the NPC standing in room, matched against arg by canonical
// id or case-insensitive display name — the same resolution rule TAKE/DROP
// use for items (RFC §8.3/8.4). A room holds at most one NPC (world.Spawn),
// so unlike items there is nothing to disambiguate between.
func resolveNPC(w *world.World, room, arg string) *world.NPC {
	loc := w.Locations[room]
	if loc == nil || loc.Spawns == nil {
		return nil
	}
	npc := w.NPCs[loc.Spawns.NPCType]
	if npc == nil {
		return nil
	}
	if npc.ID == arg || strings.EqualFold(npc.Name, arg) {
		return npc
	}
	return nil
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
// actively carrying the delivery item for, and if so closes it: the granted
// item is consumed, the reward is granted, state becomes completed (D10). ok
// reports whether a completion happened; line is the quest's Complete
// dialogue — spoken by the target, per world.QuestDialogue — meant to replace
// npc's own TalkLine for this reply, never to be combined with it.
func (h *Hub) CompleteDelivery(c *Client, npc *world.NPC) (line string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, q := range h.world.Quests {
		if q.Type != world.QuestDeliver || q.Target != npc.ID {
			continue
		}
		if c.quests[q.ID] != protocol.QuestActive || !c.inventory[q.Grants] {
			continue
		}
		delete(c.inventory, q.Grants)
		h.grantOnceLocked(c, q.Reward)
		c.quests[q.ID] = protocol.QuestCompleted
		return q.Dialogue.Complete, true
	}
	return "", false
}

// QuestInfo resolves QUEST <npc>: npc.Quest is the giver back-pointer (world
// data, checked by Validate), so the lookup is direct. The first call for a
// given quest accepts it on the spot — D10 has no separate accept step — and
// grants q.Grants, if any, straight into the inventory. ok is false if npc
// offers no quest, or the player has already completed it (406, D10).
func (h *Hub) QuestInfo(c *Client, npc *world.NPC) (q *world.Quest, description, status string, ok bool) {
	if npc.Quest == "" {
		return nil, "", "", false
	}
	q = h.world.Quests[npc.Quest]

	h.mu.Lock()
	defer h.mu.Unlock()
	switch c.quests[q.ID] {
	case protocol.QuestCompleted:
		return nil, "", "", false
	case protocol.QuestActive:
		return q, q.Dialogue.Active, protocol.QuestActive, true
	default:
		c.quests[q.ID] = protocol.QuestActive
		if q.Grants != "" {
			h.grantOnceLocked(c, q.Grants)
		}
		return q, q.Dialogue.Offer, protocol.QuestActive, true
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
