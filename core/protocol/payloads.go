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

const (
	StatusHealthy = "healthy" // not currently engaged
	StatusCombat  = "combat"  // engaged with at least one NPC
	StatusDead    = "dead"    // 0 HP, awaiting respawn
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
