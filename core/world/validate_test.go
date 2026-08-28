package world

import (
	"strings"
	"testing"
)

func mustLoad(t *testing.T) *World {
	t.Helper()
	w, err := Load(worldPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return w
}

func TestShippedWorldIsValid(t *testing.T) {
	if err := mustLoad(t).Validate(); err != nil {
		t.Fatalf("data/world.json is invalid:\n%v", err)
	}
}

func TestValidateCatches(t *testing.T) {
	cases := []struct {
		name   string
		want   string
		break_ func(*World)
	}{
		{"dangling exit", "unknown room loc.void", func(w *World) {
			w.Locations["loc.start"].Exits["north"] = "loc.void"
		}},
		{"gate on a missing exit", `not an exit`, func(w *World) {
			w.Locations["loc.door"].Requires["up"] = "item.key"
		}},
		{"gate on a missing item", "requires unknown item item.void", func(w *World) {
			w.Locations["loc.door"].Requires["north"] = "item.void"
		}},
		{"room holds unknown item", "holds unknown item item.void", func(w *World) {
			w.Locations["loc.bar"].Items = []string{"item.void"}
		}},
		{"room holds an ungettable item", "never be picked up", func(w *World) {
			w.Locations["loc.bar"].Items = []string{"item.bone"}
		}},
		{"unknown spawn", "spawns unknown npc npc.void", func(w *World) {
			w.Locations["loc.city"].Spawns.NPCType = "npc.void"
		}},
		{"enemy without hp", "enemy without positive hp", func(w *World) {
			w.NPCs["npc.hunter"].Stats = nil
		}},
		{"npc without dialogue", "no dialogue", func(w *World) {
			w.NPCs["npc.dog"].Dialogue = nil
		}},
		{"unknown drop", "drops unknown item item.void", func(w *World) {
			w.NPCs["npc.hunter"].Drops = []string{"item.void"}
		}},
		{"quest-giver without a quest", "offers no quest", func(w *World) {
			w.NPCs["npc.barman"].Quest = ""
		}},
		{"quest giver disagrees", "points at quest", func(w *World) {
			w.Quests["bone"].Giver = "npc.vendor"
		}},
		{"unknown quest target", "unknown target npc.void", func(w *World) {
			w.Quests["bone"].Target = "npc.void"
		}},
		{"kill quest on a peaceful npc", "can never be killed", func(w *World) {
			w.Quests["hunter"].Target = "npc.dog"
		}},
		{"deliver quest grants nothing", "must grant the item", func(w *World) {
			w.Quests["bone"].Grants = ""
		}},
		{"unknown quest type", `unknown type "escort"`, func(w *World) {
			w.Quests["bone"].Type = "escort"
		}},
		{"quest rewards an unknown item", "reward unknown item item.void", func(w *World) {
			w.Quests["bone"].Reward = "item.void"
		}},
		{"incomplete quest dialogue", "needs offer, active and complete", func(w *World) {
			w.Quests["bone"].Dialogue.Active = ""
		}},
		{"item from two sources", "breaking item uniqueness", func(w *World) {
			w.Quests["bone"].Reward = "item.liquor" // already lying in the bar
		}},
		{"unreachable room", "unreachable from loc.start", func(w *World) {
			delete(w.Locations["loc.camp"].Exits, "north") // nest becomes an island
		}},
		{"one-way room", "cannot walk back to loc.start", func(w *World) {
			w.Locations["loc.boss"].Exits = map[string]string{}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := mustLoad(t)
			tc.break_(w)
			err := w.Validate()
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// A corridor walked back and forth is not a circuit, however connected it is.
func TestCircuitNeedsThreeRooms(t *testing.T) {
	line := &World{
		Start: "loc.a",
		Locations: map[string]*Location{
			"loc.a": {ID: "loc.a", Name: "A", Exits: map[string]string{"north": "loc.b"}},
			"loc.b": {ID: "loc.b", Name: "B", Exits: map[string]string{"south": "loc.a"}},
		},
	}
	if line.hasCircuit() {
		t.Error("a two-room corridor must not count as a circuit")
	}
	if err := line.Validate(); err == nil || !strings.Contains(err.Error(), "not a circuit") {
		t.Errorf("Validate = %v, want a circuit complaint", err)
	}

	ring := &World{
		Start: "loc.a",
		Locations: map[string]*Location{
			"loc.a": {ID: "loc.a", Name: "A", Exits: map[string]string{"north": "loc.b"}},
			"loc.b": {ID: "loc.b", Name: "B", Exits: map[string]string{"east": "loc.c"}},
			"loc.c": {ID: "loc.c", Name: "C", Exits: map[string]string{"south": "loc.a"}},
		},
	}
	if !ring.hasCircuit() {
		t.Error("a three-room ring is a circuit")
	}
	if err := ring.Validate(); err != nil {
		t.Errorf("three-room ring should validate: %v", err)
	}
}

// Every problem is reported at once, not one server start at a time.
func TestValidateReportsEveryProblem(t *testing.T) {
	w := mustLoad(t)
	w.Locations["loc.start"].Exits["north"] = "loc.void"
	w.NPCs["npc.hunter"].Stats = nil
	w.Quests["bone"].Dialogue.Offer = ""

	err := w.Validate()
	if err == nil {
		t.Fatal("want errors")
	}
	for _, want := range []string{"loc.void", "positive hp", "needs offer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
