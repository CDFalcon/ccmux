package queue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Verbatim shape of a session's queue.json after two pre-lockfile writers
// raced: a shorter, newer document written over a longer one without
// truncation. Every Stop hook in that session then failed with
// "failed to parse queue file: invalid character '0' after top-level value"
// and no agent's status ever transitioned again.
func TestLoad_ShouldRecoverQueue_GivenTrailingGarbage(t *testing.T) {
	q, cleanup := setupTestQueue(t)
	defer cleanup()

	corrupt := "{\n  \"version\": 1,\n  \"items\": null,\n  \"counter\": 339\n}" +
		"0\",\n      \"type\": \"idle\",\n      \"agent_id\": \"f4548e65\",\n" +
		"      \"summary\": \"Throttled\"\n    }\n  ],\n  \"counter\": 340\n}\n"
	if err := os.WriteFile(q.filePath, []byte(corrupt), 0644); err != nil {
		t.Fatal(err)
	}

	items, err := q.List()
	if err != nil {
		t.Fatalf("expected recovery, got %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("recovered document has no items, got %+v", items)
	}

	// The counter continues from the recovered document and the next write
	// leaves a clean file behind.
	item, err := q.Add(ItemTypeIdle, "abc12345", "done", "")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "q340" {
		t.Fatalf("counter should continue from 339, got id %s", item.ID)
	}
	onDisk, _ := os.ReadFile(q.filePath)
	if strings.Contains(string(onDisk), "\"counter\": 339") {
		t.Fatalf("save did not clean the file: %s", onDisk)
	}
	entries, _ := os.ReadDir(filepath.Dir(q.filePath))
	if len(entries) != 2 {
		t.Fatalf("expected queue.json plus one quarantine copy, got %v", entries)
	}
}
