package protocol

type Room struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Exits       map[string]string `json:"exits"` // direction → room id
}

type LookReply struct {
	Room    Room     `json:"room"`
	Players []string `json:"players"`
	Items   []string `json:"items"`
	NPCs    []string `json:"npcs"`
}

type InventoryReply []string

type AttackReply struct {
	AttackerHP int    `json:"attacker_hp"`
	TargetHP   int    `json:"target_hp"`
	Damage     int    `json:"damage"`
	Status     string `json:"status"` // combat status, see the Status* constants
}

type StatusReply struct {
	HP     int    `json:"hp"`
	MaxHP  int    `json:"max_hp"`
	Status string `json:"status"` // see the Status* constants
}

// FleeReply carries FLEE's free counter-attack alongside the room move
// (D16): Damage and HP are the room's enemy hit taken on the way out, 0/c's
// unchanged HP when no enemy shared the room to land one.
type FleeReply struct {
	Room   string `json:"room"`
	HP     int    `json:"hp"`
	Damage int    `json:"damage"`
	Status string `json:"status"` // see the Status* constants
}

// StatusDead is only ever seen in the AttackReply of the exchange that
// caused it: respawn is synchronous, so a player's HP is never observably
// 0 afterwards (D15).
const (
	StatusHealthy = "healthy" // at max HP
	StatusCombat  = "combat"  // wounded: below max HP, no natural regen
	StatusDead    = "dead"    // 0 HP
)

type QuestReply struct {
	QuestID     string `json:"quest_id"`
	Description string `json:"description"`
	Reward      string `json:"reward"` // canonical id of the item granted on completion
	Status      string `json:"status"` // see the Quest* constants
}

type QuestEntry struct {
	QuestID  string `json:"quest_id"`
	Status   string `json:"status"`             // see the Quest* constants
	Progress string `json:"progress,omitempty"` // e.g. "1/3"
}

type QuestsReply []QuestEntry

const (
	QuestAvailable = "available" // offered by an NPC, not yet taken
	QuestActive    = "active"    // taken, objectives not all met
	QuestCompleted = "completed" // objectives met and reward granted
)
