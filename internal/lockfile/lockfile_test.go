package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquire_ShouldExcludeSecondHolder_GivenLockHeld(t *testing.T) {
	// Setup.
	t.Setenv("HOME", t.TempDir())
	target := filepath.Join(t.TempDir(), "state.json")

	first, err := Acquire(target, DefaultTimeout)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	// Execute. A second acquire must not succeed while the first is held.
	second, err := Acquire(target, 200*time.Millisecond)

	// Assert.
	if err == nil {
		Release(second)
		t.Fatal("expected the second Acquire to time out while the lock was held")
	}

	// And it must succeed once released.
	if err := Release(first); err != nil {
		t.Fatalf("Release: %v", err)
	}
	third, err := Acquire(target, DefaultTimeout)
	if err != nil {
		t.Fatalf("expected Acquire to succeed after release, got %v", err)
	}
	Release(third)
}

func TestRelease_ShouldTolerateNil(t *testing.T) {
	// Setup / Execute / Assert.
	if err := Release(nil); err != nil {
		t.Errorf("expected Release(nil) to be a no-op, got %v", err)
	}
}

// TestWithLock_ShouldSerialiseCriticalSections_GivenConcurrentCallers is the
// direct reproduction of the reported spawn race. Several launchers each did an
// unlocked read-modify-write of ~/.claude.json; with no mutual exclusion, updates
// were lost. Here every goroutine increments a counter it read moments earlier,
// so any overlap loses an increment.
func TestWithLock_ShouldSerialiseCriticalSections_GivenConcurrentCallers(t *testing.T) {
	// Setup.
	t.Setenv("HOME", t.TempDir())
	target := filepath.Join(t.TempDir(), "counter")
	if err := os.WriteFile(target, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}

	const workers = 24
	var overlaps atomic.Int64
	var inside atomic.Int64
	var wg sync.WaitGroup

	// Execute.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			WithLock(target, DefaultTimeout, func() error {
				if inside.Add(1) > 1 {
					overlaps.Add(1)
				}
				// Read-modify-write with a real gap, the shape of the bug.
				raw, err := os.ReadFile(target)
				if err != nil {
					inside.Add(-1)
					return err
				}
				var n int
				fmt.Sscanf(string(raw), "%d", &n)
				time.Sleep(time.Millisecond)
				err = WriteFileAtomic(target, []byte(fmt.Sprint(n+1)), 0o644)
				inside.Add(-1)
				return err
			})
		}()
	}
	wg.Wait()

	// Assert.
	if got := overlaps.Load(); got != 0 {
		t.Errorf("expected no overlapping critical sections, saw %d", got)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var final int
	fmt.Sscanf(string(raw), "%d", &final)
	if final != workers {
		t.Errorf("expected all %d increments to survive, got %d (lost update)", workers, final)
	}
}

// TestWriteFileAtomic_ShouldNotCollideOnScratchPath_GivenConcurrentWriters is
// the other half of the reported failure. The shell idiom this replaces used one
// fixed `$HOME/.claude.json.tmp` for every writer, so one writer's `mv` renamed
// the scratch file away and a sibling's `mv` then failed with
// "No such file or directory" — killing that launcher under `set -e`.
//
// Unique temp names make the collision impossible even with no lock at all: every
// writer must succeed and the target must always be one writer's complete output.
func TestWriteFileAtomic_ShouldNotCollideOnScratchPath_GivenConcurrentWriters(t *testing.T) {
	// Setup.
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")

	const workers = 32
	payloads := make(map[string]bool)
	for i := 0; i < workers; i++ {
		payloads[fmt.Sprintf("payload-%02d", i)] = true
	}

	var wg sync.WaitGroup
	errs := make(chan error, workers)

	// Execute.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := WriteFileAtomic(target, []byte(fmt.Sprintf("payload-%02d", i)), 0o644); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	// Assert. No writer failed...
	for err := range errs {
		t.Errorf("concurrent WriteFileAtomic failed: %v", err)
	}
	// ...and the result is exactly one writer's payload, never a mix or a
	// truncation.
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("target missing after concurrent writes: %v", err)
	}
	if !payloads[string(raw)] {
		t.Errorf("target holds torn content %q", string(raw))
	}
}

func TestWriteFileAtomic_ShouldLeaveNoTempFiles(t *testing.T) {
	// Setup.
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")

	// Execute.
	for i := 0; i < 5; i++ {
		if err := WriteFileAtomic(target, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Assert.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only the target to remain, got %v", names)
	}
}

func TestWriteFileAtomic_ShouldPreserveMode(t *testing.T) {
	// Setup.
	target := filepath.Join(t.TempDir(), "secret")

	// Execute.
	if err := WriteFileAtomic(target, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Assert.
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("expected mode 0600, got %o", got)
	}
}

// TestLockPathFor_ShouldBeStableAndDistinct keeps two different targets from
// sharing a lock (which would serialise unrelated work) while the same target
// always maps to the same lock (without which the lock does nothing).
func TestLockPathFor_ShouldBeStableAndDistinct(t *testing.T) {
	// Setup.
	t.Setenv("HOME", t.TempDir())

	// Execute.
	a1, err := lockPathFor("/some/dir/.claude.json")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := lockPathFor("/some/dir/.claude.json")
	if err != nil {
		t.Fatal(err)
	}
	b, err := lockPathFor("/other/dir/.claude.json")
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if a1 != a2 {
		t.Errorf("expected a stable lock path, got %q then %q", a1, a2)
	}
	if a1 == b {
		t.Errorf("expected distinct targets to get distinct locks, both got %q", a1)
	}
}
