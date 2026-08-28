package world

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// Validate reports every structural problem in a loaded world at once, so a
// broken world file is fixed in one pass rather than one server start per typo.
// Ids in the messages are canonical, matching what Load produced.
func (w *World) Validate() error {
	var problems []error
	bad := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf(format, args...))
	}

	for _, id := range slices.Sorted(maps.Keys(w.Locations)) {
		l := w.Locations[id]
		if l.Name == "" {
			bad("%s: no name", id)
		}
		if len(l.Exits) == 0 {
			bad("%s: no exits, players would be stuck there", id)
		}
		for _, dir := range slices.Sorted(maps.Keys(l.Exits)) {
			if target := l.Exits[dir]; w.Locations[target] == nil {
				bad("%s: exit %q leads to unknown room %s", id, dir, target)
			}
		}
		for _, dir := range slices.Sorted(maps.Keys(l.Requires)) {
			if _, ok := l.Exits[dir]; !ok {
				bad("%s: requires an item for %q, which is not an exit", id, dir)
			}
			if item := l.Requires[dir]; w.Items[item] == nil {
				bad("%s: exit %q requires unknown item %s", id, dir, item)
			}
		}
		for _, item := range l.Items {
			switch got := w.Items[item]; {
			case got == nil:
				bad("%s: holds unknown item %s", id, item)
			case !got.Obtainable:
				bad("%s: holds %s, which is not obtainable and so can never be picked up", id, item)
			}
		}
		if s := l.Spawns; s != nil {
			if w.NPCs[s.NPCType] == nil {
				bad("%s: spawns unknown npc %s", id, s.NPCType)
			}
			if s.Count < 1 {
				bad("%s: spawns %s with count %d", id, s.NPCType, s.Count)
			}
		}
	}

	for _, id := range slices.Sorted(maps.Keys(w.NPCs)) {
		npc := w.NPCs[id]
		switch npc.Role {
		case RoleDialogue, RoleQuestGiver:
		case RoleEnemy:
			if npc.Stats == nil || npc.Stats.HP < 1 {
				bad("%s: enemy without positive hp, ATTACK could never resolve", id)
			}
		default:
			bad("%s: unknown role %q", id, npc.Role)
		}
		if len(npc.Dialogue) == 0 {
			bad("%s: no dialogue, TALK would answer with an empty line", id)
		}
		for _, drop := range npc.Drops {
			if w.Items[drop] == nil {
				bad("%s: drops unknown item %s", id, drop)
			}
		}
		switch {
		case npc.Quest == "" && npc.Role == RoleQuestGiver:
			bad("%s: role is %q but it offers no quest", id, RoleQuestGiver)
		case npc.Quest != "" && npc.Role != RoleQuestGiver:
			bad("%s: offers quest %q but its role is %q", id, npc.Quest, npc.Role)
		case npc.Quest != "" && w.Quests[npc.Quest] == nil:
			bad("%s: offers unknown quest %q", id, npc.Quest)
		}
	}

	for _, id := range slices.Sorted(maps.Keys(w.Quests)) {
		q := w.Quests[id]
		giver := w.NPCs[q.Giver]
		switch {
		case giver == nil:
			bad("quest %s: unknown giver %s", id, q.Giver)
		case giver.Quest != id:
			bad("quest %s: giver %s points at quest %q instead", id, q.Giver, giver.Quest)
		}
		target := w.NPCs[q.Target]
		if target == nil {
			bad("quest %s: unknown target %s", id, q.Target)
		}

		switch q.Type {
		case QuestDeliver:
			if q.Grants == "" {
				bad("quest %s: a %s quest must grant the item to deliver", id, QuestDeliver)
			}
		case QuestKill:
			if target != nil && target.Role != RoleEnemy {
				bad("quest %s: target %s has role %q, so it can never be killed", id, q.Target, target.Role)
			}
		default:
			bad("quest %s: unknown type %q", id, q.Type)
		}

		for label, item := range map[string]string{"grants": q.Grants, "reward": q.Reward} {
			if item != "" && w.Items[item] == nil {
				bad("quest %s: %s unknown item %s", id, label, item)
			}
		}
		if q.Dialogue.Offer == "" || q.Dialogue.Active == "" || q.Dialogue.Complete == "" {
			bad("quest %s: dialogue needs offer, active and complete lines", id)
		}
	}

	problems = append(problems, w.checkItemSources()...)
	problems = append(problems, w.checkGraph()...)
	return errors.Join(problems...)
}

// checkItemSources enforces §8.1 uniqueness: an item reaching the world from
// two places could exist twice at once.
func (w *World) checkItemSources() []error {
	sources := map[string][]string{}
	add := func(item, source string) {
		if item != "" {
			sources[item] = append(sources[item], source)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(w.Locations)) {
		for _, item := range w.Locations[id].Items {
			add(item, "room "+id)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(w.NPCs)) {
		for _, item := range w.NPCs[id].Drops {
			add(item, "drop from "+id)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(w.Quests)) {
		add(w.Quests[id].Grants, "grant of quest "+id)
		add(w.Quests[id].Reward, "reward of quest "+id)
	}

	var problems []error
	for _, item := range slices.Sorted(maps.Keys(sources)) {
		if from := sources[item]; len(from) > 1 {
			problems = append(problems, fmt.Errorf("%s: enters the world from %d places (%v), breaking item uniqueness", item, len(from), from))
		}
	}
	return problems
}

// checkGraph proves the map is walkable: every room reachable from the start,
// every room able to reach it back, and at least one real loop.
func (w *World) checkGraph() []error {
	if w.Locations[w.Start] == nil {
		return []error{fmt.Errorf("start room %s not found", w.Start)}
	}

	forward := map[string][]string{}
	backward := map[string][]string{}
	for id, l := range w.Locations {
		for _, target := range l.Exits {
			if w.Locations[target] == nil {
				continue // dangling exit, already reported
			}
			forward[id] = append(forward[id], target)
			backward[target] = append(backward[target], id)
		}
	}

	var problems []error
	if unseen := missing(w, forward); len(unseen) > 0 {
		problems = append(problems, fmt.Errorf("unreachable from %s: %v", w.Start, unseen))
	}
	if unseen := missing(w, backward); len(unseen) > 0 {
		problems = append(problems, fmt.Errorf("cannot walk back to %s from: %v", w.Start, unseen))
	}
	if !w.hasCircuit() {
		problems = append(problems, errors.New("no loop of three or more rooms: the map is a line, not a circuit"))
	}
	return problems
}

// missing walks adj from the start room and returns the rooms it never sees.
func missing(w *World, adj map[string][]string) []string {
	seen := map[string]bool{w.Start: true}
	for queue := []string{w.Start}; len(queue) > 0; {
		id := queue[0]
		queue = queue[1:]
		for _, next := range adj[id] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	var unseen []string
	for _, id := range slices.Sorted(maps.Keys(w.Locations)) {
		if !seen[id] {
			unseen = append(unseen, id)
		}
	}
	return unseen
}

// hasCircuit reports whether the exit graph holds a directed cycle of at least
// three rooms — a loop the player can walk, as opposed to a corridor walked
// back and forth. Depth-first with the current path marked; worlds are small
// enough that the unbounded search never matters.
func (w *World) hasCircuit() bool {
	const minRooms = 3
	depth := make(map[string]int, len(w.Locations)) // 0 means "not on the current path"

	var walk func(id string, d int) bool
	walk = func(id string, d int) bool {
		depth[id] = d
		for _, next := range w.Locations[id].Exits {
			if w.Locations[next] == nil {
				continue
			}
			if at := depth[next]; at > 0 {
				if d-at+1 >= minRooms {
					return true
				}
				continue
			}
			if walk(next, d+1) {
				return true
			}
		}
		depth[id] = 0
		return false
	}

	for _, id := range slices.Sorted(maps.Keys(w.Locations)) {
		if walk(id, 1) {
			return true
		}
	}
	return false
}
