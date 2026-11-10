package queue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/CDFalcon/ccmux/internal/lockfile"
)

type Queue struct {
	mu       sync.Mutex
	filePath string
}

func NewQueue(sessionID string) (*Queue, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	sessionDir := filepath.Join(homeDir, ".ccmux", "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	return &Queue{
		filePath: filepath.Join(sessionDir, "queue.json"),
	}, nil
}

func (q *Queue) load() (*queueData, error) {
	data := &queueData{
		Items: make([]*QueueItem, 0),
	}

	raw, err := lockfile.ReadJSON(q.filePath)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read queue file: %w", err)
	}

	var envelope struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("failed to parse queue file: %w", err)
	}

	if envelope.Version < CurrentSchemaVersion {
		raw, err = migrations.Migrate(raw, envelope.Version, CurrentSchemaVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to migrate queue file: %w", err)
		}
	}

	if err := json.Unmarshal(raw, data); err != nil {
		return nil, fmt.Errorf("failed to parse queue file: %w", err)
	}

	data.Version = CurrentSchemaVersion

	return data, nil
}

func (q *Queue) save(data *queueData) error {
	data.Version = CurrentSchemaVersion

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal queue: %w", err)
	}

	if err := lockfile.WriteFileAtomic(q.filePath, bytes, 0644); err != nil {
		return fmt.Errorf("failed to write queue file: %w", err)
	}

	return nil
}

func (q *Queue) Add(itemType ItemType, agentID, summary, details string) (*QueueItem, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	lock, err := lockfile.Acquire(q.filePath, lockfile.DefaultTimeout)
	if err != nil {
		return nil, err
	}
	defer lockfile.Release(lock)

	data, err := q.load()
	if err != nil {
		return nil, err
	}

	// An agent holds at most one queue item: the queue says "this agent
	// needs you", and a second line for the same agent says nothing new.
	// Callers check for an existing item before adding, but they check a
	// snapshot taken outside this lock, so racing writers (the TUI's CI poll
	// and the `pr-ready` hook, or two TUIs on one session) used to stack
	// duplicates. Enforce it here, where the check and the write are atomic.
	//
	// Re-raising the same kind of item refreshes it in place: it keeps its
	// ID, so a caller still holding that ID can remove it, and its place and
	// age, since the agent has needed attention since then. A different kind
	// replaces it: the agent's state moved on.
	var existing *QueueItem
	kept := data.Items[:0]
	for _, it := range data.Items {
		if it.AgentID != agentID {
			kept = append(kept, it)
			continue
		}
		if existing == nil && it.Type == itemType {
			it.Summary = summary
			it.Details = details
			existing = it
			kept = append(kept, it)
		}
	}
	data.Items = kept

	item := existing
	if item == nil {
		data.Counter++
		item = &QueueItem{
			ID:        fmt.Sprintf("q%d", data.Counter),
			Type:      itemType,
			AgentID:   agentID,
			Summary:   summary,
			Details:   details,
			Timestamp: time.Now(),
		}
		data.Items = append(data.Items, item)
	}

	if err := q.save(data); err != nil {
		return nil, err
	}

	return item, nil
}

// Reconcile drops items the queue should not be holding: items for agents
// that are no longer registered, and all but the newest item of any agent
// that has more than one. It reports whether it changed anything, and writes
// the file only then.
//
// live is the set of registered agent IDs, read at listedAt. Only items
// raised before listedAt are judged against it, so an agent created (and
// queued) after that read is not mistaken for a departed one.
func (q *Queue) Reconcile(live map[string]bool, listedAt time.Time) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	lock, err := lockfile.Acquire(q.filePath, lockfile.DefaultTimeout)
	if err != nil {
		return false, err
	}
	defer lockfile.Release(lock)

	data, err := q.load()
	if err != nil {
		return false, err
	}

	// Walk newest first so the first item seen for an agent is the one kept:
	// later items reflect the agent's later state.
	seen := make(map[string]bool)
	keep := make([]bool, len(data.Items))
	changed := false
	for i := len(data.Items) - 1; i >= 0; i-- {
		it := data.Items[i]
		if seen[it.AgentID] || (!live[it.AgentID] && it.Timestamp.Before(listedAt)) {
			changed = true
			continue
		}
		seen[it.AgentID] = true
		keep[i] = true
	}
	if !changed {
		return false, nil
	}

	kept := make([]*QueueItem, 0, len(data.Items))
	for i, it := range data.Items {
		if keep[i] {
			kept = append(kept, it)
		}
	}
	data.Items = kept

	if err := q.save(data); err != nil {
		return false, err
	}
	return true, nil
}

func (q *Queue) List() ([]*QueueItem, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	data, err := q.load()
	if err != nil {
		return nil, err
	}

	return data.Items, nil
}

func (q *Queue) ListByType(itemType ItemType) ([]*QueueItem, error) {
	items, err := q.List()
	if err != nil {
		return nil, err
	}

	var filtered []*QueueItem
	for _, item := range items {
		if item.Type == itemType {
			filtered = append(filtered, item)
		}
	}

	return filtered, nil
}

func (q *Queue) Remove(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	lock, err := lockfile.Acquire(q.filePath, lockfile.DefaultTimeout)
	if err != nil {
		return err
	}
	defer lockfile.Release(lock)

	data, err := q.load()
	if err != nil {
		return err
	}

	var newItems []*QueueItem
	found := false
	for _, item := range data.Items {
		if item.ID == id {
			found = true
			continue
		}
		newItems = append(newItems, item)
	}

	if !found {
		return fmt.Errorf("queue item with ID %s not found", id)
	}

	data.Items = newItems

	return q.save(data)
}

func (q *Queue) RemoveByAgent(agentID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	lock, err := lockfile.Acquire(q.filePath, lockfile.DefaultTimeout)
	if err != nil {
		return err
	}
	defer lockfile.Release(lock)

	data, err := q.load()
	if err != nil {
		return err
	}

	var newItems []*QueueItem
	for _, item := range data.Items {
		if item.AgentID == agentID {
			continue
		}
		newItems = append(newItems, item)
	}

	data.Items = newItems

	return q.save(data)
}

func (q *Queue) RemoveByAgentAndType(agentID string, itemType ItemType) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	lock, err := lockfile.Acquire(q.filePath, lockfile.DefaultTimeout)
	if err != nil {
		return err
	}
	defer lockfile.Release(lock)

	data, err := q.load()
	if err != nil {
		return err
	}

	var newItems []*QueueItem
	for _, item := range data.Items {
		if item.AgentID == agentID && item.Type == itemType {
			continue
		}
		newItems = append(newItems, item)
	}

	data.Items = newItems

	return q.save(data)
}

func (q *Queue) Clear() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	lock, err := lockfile.Acquire(q.filePath, lockfile.DefaultTimeout)
	if err != nil {
		return err
	}
	defer lockfile.Release(lock)

	data := &queueData{
		Items:   make([]*QueueItem, 0),
		Counter: 0,
	}

	return q.save(data)
}

func (q *Queue) Get(id string) (*QueueItem, error) {
	items, err := q.List()
	if err != nil {
		return nil, err
	}

	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}

	return nil, fmt.Errorf("queue item with ID %s not found", id)
}
