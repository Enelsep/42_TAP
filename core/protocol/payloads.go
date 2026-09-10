package protocol

type Room struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Exits       map[string]string `json:"exits"`
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
	Status     string `json:"status"`
}

type StatusReply struct {
	HP     int    `json:"hp"`
	MaxHP  int    `json:"max_hp"`
	Status string `json:"status"`
}

type FleeReply struct {
	Room   string `json:"room"`
	HP     int    `json:"hp"`
	Damage int    `json:"damage"`
	Status string `json:"status"`
}

const (
	StatusHealthy = "healthy"
	StatusCombat  = "combat"
	StatusDead    = "dead"
)

type QuestReply struct {
	QuestID     string `json:"quest_id"`
	Description string `json:"description"`
	Reward      string `json:"reward"`
	Status      string `json:"status"`
}

type QuestEntry struct {
	QuestID  string `json:"quest_id"`
	Status   string `json:"status"`
	Progress string `json:"progress,omitempty"`
}

type QuestsReply []QuestEntry

const (
	QuestAvailable = "available"
	QuestActive    = "active"
	QuestCompleted = "completed"
)
