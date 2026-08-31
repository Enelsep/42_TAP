package world

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

const (
	PrefixLocation = "loc."
	PrefixItem     = "item."
	PrefixNPC      = "npc."
)

const StartKey = "start"

const (
	RoleDialogue   = "dialogue"
	RoleQuestGiver = "quest-giver"
	RoleEnemy      = "enemy"
)

const (
	QuestDeliver = "deliver"
	QuestKill    = "kill"
)

type World struct {
	Locations map[string]*Location `json:"locations"`
	Items     map[string]*Item     `json:"items"`
	NPCs      map[string]*NPC      `json:"npcs"`
	Quests    map[string]*Quest    `json:"quests"`

	Start string `json:"-"`
}

type Location struct {
	ID          string            `json:"-"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Exits       map[string]string `json:"exits"`              // direction → location id
	Requires    map[string]string `json:"requires,omitempty"` // direction → item id the player must carry
	Items       []string          `json:"items,omitempty"`    // initial placement only
	Spawns      *Spawn            `json:"spawns,omitempty"`
}

type Spawn struct {
	NPCType string `json:"npc_type"`
	Count   int    `json:"count"`
}

type Item struct {
	ID          string `json:"-"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Obtainable  bool   `json:"obtainable"` // false = cannot be taken off the floor, only granted
}

type NPC struct {
	ID          string   `json:"-"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Description string   `json:"description"`
	Dialogue    []string `json:"dialogue"` // TALK lines, independent of quest state
	Stats       *Stats   `json:"stats,omitempty"`
	Drops       []string `json:"drops,omitempty"` // released into the room on death
	Quest       string   `json:"quest,omitempty"` // quest id this NPC offers
}

type Stats struct {
	HP     int `json:"hp"`
	Damage int `json:"damage"`
}

type Quest struct {
	ID       string        `json:"-"`
	Name     string        `json:"name"`
	Type     string        `json:"type"`
	Giver    string        `json:"giver"`
	Target   string        `json:"target"`           // NPC to deliver to, or to kill
	Grants   string        `json:"grants,omitempty"` // item handed over on accept
	Reward   string        `json:"reward"`
	Dialogue QuestDialogue `json:"dialogue"`
}

// QuestDialogue is keyed by quest state. Complete is spoken by the target for a
// deliver quest and by the giver for a kill quest.
type QuestDialogue struct {
	Offer    string `json:"offer"`
	Active   string `json:"active"`
	Complete string `json:"complete"`
}

// Load reads and canonicalises a world file. It does not check that references
// resolve — that is Validate's job, and the server calls both at startup.
func Load(path string) (*World, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		World *World `json:"world"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // a mistyped key is a silent world bug otherwise
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if file.World == nil {
		return nil, fmt.Errorf(`%s: missing top-level "world" object`, path)
	}
	file.World.canonicalize()
	return file.World, nil
}

// canonicalize re-keys every map by canonical id and rewrites every reference
// to match, so that after Load the bare ids of the JSON file appear nowhere.
func (w *World) canonicalize() {
	w.Start = PrefixLocation + StartKey

	locations := make(map[string]*Location, len(w.Locations))
	for key, l := range w.Locations {
		l.ID = PrefixLocation + key
		for dir, target := range l.Exits {
			l.Exits[dir] = PrefixLocation + target
		}
		for dir, item := range l.Requires {
			l.Requires[dir] = PrefixItem + item
		}
		for i, item := range l.Items {
			l.Items[i] = PrefixItem + item
		}
		if l.Spawns != nil {
			l.Spawns.NPCType = PrefixNPC + l.Spawns.NPCType
		}
		locations[l.ID] = l
	}
	w.Locations = locations

	items := make(map[string]*Item, len(w.Items))
	for key, item := range w.Items {
		item.ID = PrefixItem + key
		items[item.ID] = item
	}
	w.Items = items

	npcs := make(map[string]*NPC, len(w.NPCs))
	for key, npc := range w.NPCs {
		npc.ID = PrefixNPC + key
		for i, drop := range npc.Drops {
			npc.Drops[i] = PrefixItem + drop
		}
		npcs[npc.ID] = npc // npc.Quest already holds a bare quest id
	}
	w.NPCs = npcs

	for key, quest := range w.Quests {
		quest.ID = key
		quest.Giver = PrefixNPC + quest.Giver
		quest.Target = PrefixNPC + quest.Target
		if quest.Grants != "" {
			quest.Grants = PrefixItem + quest.Grants
		}
		if quest.Reward != "" {
			quest.Reward = PrefixItem + quest.Reward
		}
	}
}
