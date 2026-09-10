package server

import (
	"slices"
	"strings"

	"github.com/Enelsep/42_TAP/core/protocol"
	"github.com/Enelsep/42_TAP/core/world"
)

func npcAt(w *world.World, room string) *world.NPC {
	loc := w.Locations[room]
	if loc == nil || loc.Spawns == nil {
		return nil
	}
	return w.NPCs[loc.Spawns.NPCType]
}

func (h *Hub) TalkLine(npc *world.NPC) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := h.npcTalk[npc.ID] % len(npc.Dialogue)
	h.npcTalk[npc.ID]++
	return npc.Dialogue[i]
}

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
		h.spawnLocked(c, q.Grants)
		return q, q.Dialogue.Active, protocol.QuestActive, false, true
	default:
		c.quests[q.ID] = protocol.QuestActive
		h.spawnLocked(c, q.Grants)
		return q, q.Dialogue.Offer, protocol.QuestActive, true, true
	}
}

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
