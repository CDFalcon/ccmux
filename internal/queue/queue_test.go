package queue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func setupTestQueue(t *testing.T) (*Queue, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "ccmux-queue-test")
	if err != nil {
		t.Fatal(err)
	}

	q := &Queue{
		filePath: filepath.Join(tmpDir, "queue.json"),
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return q, cleanup
}

func TestAdd_ShouldCreateQueueItem_GivenValidInput(t *testing.T) {
	// Setup.
	q, cleanup := setupTestQueue(t)
	defer cleanup()

	// Execute.
	item, err := q.Add(ItemTypeIdle, "agent-1", "Agent idle", "Details here")

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.ID == "" {
		t.Error("expected item ID to be set")
	}
	if item.Type != ItemTypeIdle {
		t.Errorf("expected type %s, got %s", ItemTypeIdle, item.Type)
	}
	if item.AgentID != "agent-1" {
		t.Errorf("expected agent ID agent-1, got %s", item.AgentID)
	}
}

func TestList_ShouldReturnAllItems_GivenMultipleAdds(t *testing.T) {
	// Setup.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	q.Add(ItemTypeIdle, "agent-1", "Idle 1", "")
	q.Add(ItemTypePRReady, "agent-2", "PR ready", "")

	// Execute.
	items, err := q.List()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestListByType_ShouldFilterByType_GivenMixedItems(t *testing.T) {
	// Setup.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	q.Add(ItemTypeIdle, "agent-1", "Idle", "")
	q.Add(ItemTypePRReady, "agent-2", "PR ready", "")
	q.Add(ItemTypeIdle, "agent-3", "Another idle", "")

	// Execute.
	items, err := q.ListByType(ItemTypeIdle)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 idle items, got %d", len(items))
	}
}

func TestRemove_ShouldDeleteItem_GivenValidID(t *testing.T) {
	// Setup.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	item, _ := q.Add(ItemTypeIdle, "agent-1", "Idle", "")

	// Execute.
	err := q.Remove(item.ID)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items, _ := q.List()
	if len(items) != 0 {
		t.Errorf("expected 0 items after removal, got %d", len(items))
	}
}

func TestRemoveByAgent_ShouldDeleteAllAgentItems_GivenAgentID(t *testing.T) {
	// Setup.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	q.Add(ItemTypeIdle, "agent-1", "Idle 1", "")
	q.Add(ItemTypePRReady, "agent-1", "PR ready", "")
	q.Add(ItemTypeIdle, "agent-2", "Idle 2", "")

	// Execute.
	err := q.RemoveByAgent("agent-1")

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items, _ := q.List()
	if len(items) != 1 {
		t.Errorf("expected 1 item after removal, got %d", len(items))
	}
	if items[0].AgentID != "agent-2" {
		t.Errorf("expected remaining item from agent-2, got %s", items[0].AgentID)
	}
}

func TestRemoveByAgentAndType_ShouldDeleteMatchingItems_GivenAgentIDAndType(t *testing.T) {
	// Setup.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	q.Add(ItemTypePRReady, "agent-1", "PR ready 1", "https://github.com/pr/1")
	q.Add(ItemTypeIdle, "agent-2", "Idle agent-2", "")
	q.Add(ItemTypePRReady, "agent-3", "PR ready 3", "https://github.com/pr/3")

	// Execute. The second call names a type agent-2 does not have.
	err := q.RemoveByAgentAndType("agent-1", ItemTypePRReady)
	if err == nil {
		err = q.RemoveByAgentAndType("agent-2", ItemTypePRReady)
	}

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items, _ := q.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 items after removal, got %d", len(items))
	}
	if items[0].AgentID != "agent-2" || items[1].AgentID != "agent-3" {
		t.Errorf("expected agent-2 and agent-3 to remain, got %s and %s", items[0].AgentID, items[1].AgentID)
	}
}

func TestClear_ShouldRemoveAllItems_GivenPopulatedQueue(t *testing.T) {
	// Setup.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	q.Add(ItemTypeIdle, "agent-1", "Idle", "")
	q.Add(ItemTypePRReady, "agent-2", "PR ready", "")

	// Execute.
	err := q.Clear()

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items, _ := q.List()
	if len(items) != 0 {
		t.Errorf("expected 0 items after clear, got %d", len(items))
	}
}

func TestAdd_ShouldRefreshExistingItemInPlace_GivenSameAgentAndType(t *testing.T) {
	// Setup. Two writers both decide agent-1's PR is ready: the TUI's CI poll
	// and the `pr-ready` hook. Each used to append its own item.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	first, _ := q.Add(ItemTypePRReady, "agent-1", "Old title", "https://github.com/pr/1")
	q.Add(ItemTypeIdle, "agent-2", "Idle", "")

	// Execute.
	second, err := q.Add(ItemTypePRReady, "agent-1", "New title", "https://github.com/pr/1")

	// Assert. One item, same ID and place, carrying the newer text.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("expected the existing item %s to be reused, got %s", first.ID, second.ID)
	}
	items, _ := q.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ID != first.ID || items[0].Summary != "New title" {
		t.Errorf("expected agent-1's item first with the new summary, got %+v", items[0])
	}
	if !items[0].Timestamp.Equal(first.Timestamp) {
		t.Errorf("expected the item to keep its age %v, got %v", first.Timestamp, items[0].Timestamp)
	}
}

func TestAdd_ShouldReplaceAgentItem_GivenDifferentType(t *testing.T) {
	// Setup.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	q.Add(ItemTypeIdle, "agent-1", "Agent finished (no PR)", "")
	q.Add(ItemTypeIdle, "agent-2", "Idle", "")

	// Execute.
	item, err := q.Add(ItemTypePRReady, "agent-1", "PR ready", "https://github.com/pr/1")

	// Assert. The idle item is gone; the PR item is the newest in the queue.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items, _ := q.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[1].ID != item.ID || items[1].Type != ItemTypePRReady {
		t.Errorf("expected agent-1's PR item last, got %+v", items[1])
	}
	if items[0].AgentID != "agent-2" {
		t.Errorf("expected agent-2's item to be untouched, got %+v", items[0])
	}
}

func TestAdd_ShouldKeepOneItemPerAgent_GivenConcurrentWriters(t *testing.T) {
	// Setup. Separate Queue values on one file stand in for separate
	// processes: they share the file lock but not the in-process mutex.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	const writers = 8

	// Execute.
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := &Queue{filePath: q.filePath}
			if _, err := w.Add(ItemTypePRReady, "agent-1", fmt.Sprintf("PR ready %d", i), "https://github.com/pr/1"); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// Assert.
	items, _ := q.List()
	if len(items) != 1 {
		t.Errorf("expected 1 item for agent-1, got %d", len(items))
	}
}

func TestReconcile_ShouldDropItems_GivenAgentNoLongerRegistered(t *testing.T) {
	// Setup.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	q.Add(ItemTypeIdle, "gone", "Agent finished (no PR)", "")
	q.Add(ItemTypeDead, "also-gone", "Agent failed - kill or restart", "")
	q.Add(ItemTypePRReady, "alive", "PR ready", "https://github.com/pr/1")
	listedAt := time.Now()

	// Execute.
	changed, err := q.Reconcile(map[string]bool{"alive": true}, listedAt)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected Reconcile to report a change")
	}
	items, _ := q.List()
	if len(items) != 1 || items[0].AgentID != "alive" {
		t.Errorf("expected only the live agent's item, got %+v", items)
	}
}

func TestReconcile_ShouldKeepItem_GivenItRaisedAfterAgentsWereListed(t *testing.T) {
	// Setup. The caller read the registry, then a new agent registered and
	// queued an item before Reconcile ran. The stale agent list does not
	// know it, but it is not gone.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	listedAt := time.Now().Add(-time.Second)
	q.Add(ItemTypeDead, "new-agent", "Agent failed - kill or restart", "spawn failed")

	// Execute.
	changed, err := q.Reconcile(map[string]bool{}, listedAt)

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change")
	}
	items, _ := q.List()
	if len(items) != 1 {
		t.Errorf("expected the new agent's item to survive, got %d items", len(items))
	}
}

func TestReconcile_ShouldKeepNewestItem_GivenDuplicateAgentItems(t *testing.T) {
	// Setup. A queue file written before Add kept one item per agent.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	old := time.Now().Add(-time.Hour)
	data := queueData{
		Version: CurrentSchemaVersion,
		Counter: 4,
		Items: []*QueueItem{
			{ID: "q1", Type: ItemTypeIdle, AgentID: "agent-1", Summary: "Agent idle - waiting for input", Timestamp: old},
			{ID: "q2", Type: ItemTypePRReady, AgentID: "agent-2", Summary: "PR ready", Timestamp: old},
			{ID: "q3", Type: ItemTypePRReady, AgentID: "agent-1", Summary: "PR ready", Timestamp: old},
			{ID: "q4", Type: ItemTypePRReady, AgentID: "agent-1", Summary: "PR ready", Timestamp: old},
		},
	}
	raw, _ := json.Marshal(data)
	if err := os.WriteFile(q.filePath, raw, 0644); err != nil {
		t.Fatal(err)
	}

	// Execute.
	changed, err := q.Reconcile(map[string]bool{"agent-1": true, "agent-2": true}, time.Now())

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected Reconcile to report a change")
	}
	items, _ := q.List()
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	if fmt.Sprint(ids) != "[q2 q4]" {
		t.Errorf("expected [q2 q4], got %v", ids)
	}
}

func TestReconcile_ShouldNotRewriteFile_GivenCleanQueue(t *testing.T) {
	// Setup. The TUI reconciles on every refresh tick; a clean queue must not
	// cost a write each time.
	q, cleanup := setupTestQueue(t)
	defer cleanup()
	q.Add(ItemTypeIdle, "agent-1", "Idle", "")
	before, err := os.Stat(q.filePath)
	if err != nil {
		t.Fatal(err)
	}
	past := before.ModTime().Add(-time.Minute)
	if err := os.Chtimes(q.filePath, past, past); err != nil {
		t.Fatal(err)
	}

	// Execute.
	changed, err := q.Reconcile(map[string]bool{"agent-1": true}, time.Now())

	// Assert.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change")
	}
	after, _ := os.Stat(q.filePath)
	if !after.ModTime().Equal(past) {
		t.Errorf("expected the queue file to be left alone, mtime moved to %v", after.ModTime())
	}
}
