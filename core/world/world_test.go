package world

import (
	"os"
	"path/filepath"
	"testing"
)

const worldPath = "../../data/world.json"

func TestLoadCanonicalises(t *testing.T) {
	w, err := Load(worldPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if w.Start != "loc.start" || w.Locations[w.Start] == nil {
		t.Fatalf("start room = %q, present = %v", w.Start, w.Locations[w.Start] != nil)
	}

	door := w.Locations["loc.door"]
	if door == nil {
		t.Fatal("loc.door missing")
	}
	if got := door.Exits["north"]; got != "loc.boss" {
		t.Errorf("door exit north = %q, want loc.boss", got)
	}
	if got := door.Requires["north"]; got != "item.key" {
		t.Errorf("door requires north = %q, want item.key", got)
	}
	if got := w.Locations["loc.bar"].Items; len(got) != 1 || got[0] != "item.liquor" {
		t.Errorf("bar items = %v, want [item.liquor]", got)
	}
	if got := w.Locations["loc.city"].Spawns.NPCType; got != "npc.guard" {
		t.Errorf("city spawn = %q, want npc.guard", got)
	}

	hunter := w.NPCs["npc.hunter"]
	if hunter == nil {
		t.Fatal("npc.hunter missing")
	}
	if hunter.Role != RoleEnemy || hunter.Stats == nil || hunter.Stats.HP != 40 {
		t.Errorf("hunter = %+v, %+v", hunter, hunter.Stats)
	}
	if got := hunter.Drops; len(got) != 1 || got[0] != "item.key" {
		t.Errorf("hunter drops = %v, want [item.key]", got)
	}

	// Quest ids stay bare, and the NPC back-pointer uses the same bare id.
	if got := w.NPCs["npc.barman"].Quest; got != "bone" {
		t.Errorf("barman quest = %q, want bone", got)
	}
	bone := w.Quests["bone"]
	if bone == nil {
		t.Fatal("quest bone missing")
	}
	if bone.ID != "bone" || bone.Type != QuestDeliver {
		t.Errorf("quest = %+v", bone)
	}
	if bone.Giver != "npc.barman" || bone.Target != "npc.dog" {
		t.Errorf("giver = %q, target = %q", bone.Giver, bone.Target)
	}
	if bone.Grants != "item.bone" || bone.Reward != "item.crysknife" {
		t.Errorf("grants = %q, reward = %q", bone.Grants, bone.Reward)
	}
	if bone.Dialogue.Offer == "" || bone.Dialogue.Active == "" || bone.Dialogue.Complete == "" {
		t.Errorf("quest dialogue incomplete: %+v", bone.Dialogue)
	}

	for id, item := range w.Items {
		if item.ID != id {
			t.Errorf("item keyed %q carries ID %q", id, item.ID)
		}
	}
}

// The shipped world must satisfy the subject's minimums.
func TestWorldMeetsRequirements(t *testing.T) {
	w, err := Load(worldPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(w.Locations) < 8 {
		t.Errorf("%d rooms, want at least 8", len(w.Locations))
	}
	if len(w.Items) < 4 {
		t.Errorf("%d items, want at least 4", len(w.Items))
	}
	if len(w.Quests) < 2 {
		t.Errorf("%d quests, want at least 2", len(w.Quests))
	}
	obtainable := 0
	for _, item := range w.Items {
		if item.Obtainable {
			obtainable++
		}
	}
	if obtainable < 2 {
		t.Errorf("%d obtainable items, want at least 2", obtainable)
	}
	roles := map[string]bool{}
	for _, npc := range w.NPCs {
		roles[npc.Role] = true
	}
	for _, want := range []string{RoleDialogue, RoleQuestGiver, RoleEnemy} {
		if !roles[want] {
			t.Errorf("no NPC with role %q", want)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("missing file must fail")
	}

	cases := map[string]string{
		"unknown field": `{"world":{"locations":{"start":{"name":"n","descriptoin":"typo"}}}}`,
		"no world key":  `{"locations":{}}`,
		"not json":      `{`,
	}
	for name, body := range cases {
		path := filepath.Join(t.TempDir(), "world.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
