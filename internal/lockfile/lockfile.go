// Package lockfile provides advisory cross-process file locking plus atomic
// file replacement.
//
// ccmux is heavily multi-process: the TUI, every `ccmux spawn`/`task`, every
// `ccmux register-agent`, and every Claude Stop hook are separate PIDs that all
// read-modify-write the same JSON state files. Every store in the repo protected
// those writes with an in-process sync.Mutex only, which does nothing across
// processes, and wrote with a bare os.WriteFile.
//
// The visible failure was in the launcher's `~/.claude.json` pretrust step,
// which shelled out to:
//
//	jq ... "$CLAUDE_JSON" > "${CLAUDE_JSON}.tmp" && mv "${CLAUDE_JSON}.tmp" "$CLAUDE_JSON"
//
// Two defects compounded there. The temp filename was fixed, so concurrent
// spawns raced on the *same* scratch path: one spawn's `mv` renamed the file
// away, and the other's then failed with
//
//	mv: rename /Users/x/.claude.json.tmp to /Users/x/.claude.json: No such file or directory
//
// which, under the launcher's `set -e`, killed the pane before
// `ccmux register-agent` ever ran — leaving an agent stuck in `spawning` with an
// empty branch_name forever. And even when both renames succeeded, both jq
// processes had read the same pre-image, so the last writer silently dropped the
// other worktree's trust entry.
//
// Acquire fixes the lost update; WriteFileAtomic fixes the scratch-path
// collision (a unique temp name per writer, same directory so the rename is
// atomic).
//
// Scope note: flock is advisory and only binds processes that ask for it. A live
// `claude` process rewriting ~/.claude.json does not take this lock, so it can
// still lose an interleaved update. What this package guarantees is that ccmux
// never corrupts the file, never removes it, and never races against itself.
package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// DefaultTimeout bounds how long a caller waits for a contended lock. Spawns are
// interactive, so this is short enough that a wedged holder surfaces as an error
// rather than a hang, and long enough to absorb a burst of concurrent spawns.
const DefaultTimeout = 20 * time.Second

// pollInterval is how often Acquire retries a contended lock. flock offers no
// timed wait, so we use the non-blocking form in a loop.
const pollInterval = 25 * time.Millisecond

// Lock is a held advisory lock. Release must be called; the zero value is not
// usable.
type Lock struct {
	f *os.File
}

// Acquire takes an exclusive advisory lock guarding the resource at path,
// blocking until it is held or timeout elapses.
//
// The lock lives in ~/.ccmux/locks/ rather than beside the target, so locking a
// file in someone else's directory (like ~/.claude.json) leaves no litter there.
func Acquire(path string, timeout time.Duration) (*Lock, error) {
	lockPath, err := lockPathFor(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create lock directory: %w", err)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", lockPath, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{f: f}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, fmt.Errorf("failed to lock %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("timed out after %s waiting for lock on %s", timeout, path)
		}
		time.Sleep(pollInterval)
	}
}

// Release unlocks and closes the lock file. Safe to call on a nil Lock.
func Release(l *Lock) error {
	if l == nil || l.f == nil {
		return nil
	}
	// Closing the descriptor releases the flock; unlock explicitly anyway so the
	// intent is obvious and the release is not dependent on close semantics.
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}

// WithLock runs fn while holding an exclusive lock on path.
func WithLock(path string, timeout time.Duration, fn func() error) error {
	l, err := Acquire(path, timeout)
	if err != nil {
		return err
	}
	defer Release(l)
	return fn()
}

// WriteFileAtomic replaces path's contents with data, atomically.
//
// It writes to a uniquely named temp file in the same directory (so the rename
// stays within one filesystem and is therefore atomic) and renames it over the
// target. A reader never observes a partial write, and — unlike the
// fixed-`.tmp`-name shell idiom this replaces — two concurrent writers cannot
// collide on the scratch path and delete each other's work.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// Clean up the temp file on any failure path; harmless once renamed.
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	// fsync before rename so a crash cannot leave the target pointing at
	// unflushed content.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("failed to chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to rename %s to %s: %w", tmpName, path, err)
	}
	return nil
}

var unsafeLockChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// lockPathFor maps a target path to a stable lock file under ~/.ccmux/locks/.
// The hash makes it collision-free; the readable suffix makes `ls` useful when
// diagnosing a stuck lock.
func lockPathFor(path string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	sum := sha256.Sum256([]byte(abs))
	readable := unsafeLockChars.ReplaceAllString(filepath.Base(abs), "_")
	readable = strings.Trim(readable, "._-")
	if readable == "" {
		readable = "target"
	}
	name := fmt.Sprintf("%s-%s.lock", readable, hex.EncodeToString(sum[:6]))
	return filepath.Join(homeDir, ".ccmux", "locks", name), nil
}
