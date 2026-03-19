package queue

import "time"

const CurrentSchemaVersion = 1

type ItemType string

const (
	ItemTypePRReady ItemType = "pr_ready"
	ItemTypeIdle    ItemType = "idle"
	ItemTypeDead    ItemType = "dead"
)

type QueueItem struct {
	ID        string    `json:"id"`
	Type      ItemType  `json:"type"`
	AgentID   string    `json:"agent_id"`
	Summary   string    `json:"summary"`
	Details   string    `json:"details"`
	Timestamp time.Time `json:"timestamp"`
}

type queueData struct {
	Version int          `json:"version"`
	Items   []*QueueItem `json:"items"`
	Counter int          `json:"counter"`
}
