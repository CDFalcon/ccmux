package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The agent list's "Disk" column is measured by shelling out, once per agent
// per refresh, in one of two ways:
//
//   - plain worktrees: `du` over the worktree (getDiskUsage, platform files).
//   - `use_fast_worktrees` (rift) worktrees: du would report the whole source
//     repo for every agent, since a rift snapshot shares nearly all of its
//     blocks. So instead we ask git which files actually differ —
//     `git diff --name-only HEAD` plus `git ls-files --others
//     --exclude-standard` — and sum their sizes.
//
// Both are microseconds on a small clean repo and pathological on a large or
// LFS-heavy one, where every invocation walks the tree and consults the LFS
// filter for each pointer file.
//
// Before this file existed, the call site ran them synchronously and the TUI
// refresh that drives it is re-armed by an unconditional 2s tick
// (tickCmd -> refreshCmd, tui.go). A measurement slower than the tick interval
// was therefore re-issued *on top of itself*, forever, with nothing bounding
// the pile. Measured on a real machine running a git-LFS-heavy repo with ~25
// agents registered over two days:
//
//	load average peaking near 1000, `uptime` itself taking >60s, and
//	concurrently 924 `git ls-files`, 530 `git diff` and 410
//	`git-lfs filter-process` processes alive. 552 of the `git diff`
//	processes belonged to a single worktree whose agent had merged its PR
//	hours earlier — deleting that one directory dropped all 552 at once.
//
// diskProbe makes that shape impossible by construction:
//
//   - Single-flight per worktree. While a measurement for a path is running,
//     further requests for that path return the last known value instead of
//     forking a second one. This is the hard ceiling: at most one measurement
//     in flight per worktree, no matter how often the refresh loop asks.
//   - Non-blocking. Sample never waits on the subprocess; it returns the
//     cached figure and starts a background measurement only when one is due.
//     A wedged git can no longer stall the whole refresh and make the next
//     tick's work pile up behind it.
//   - Hard deadline with process-group reaping. A measurement that outlives
//     probeTimeout is killed along with its children, so a hung git-lfs filter
//     cannot hold a single-flight slot forever.
//   - Adaptive backoff. A measurement that took d earns a quiet period
//     proportional to d, so an expensive repo is sampled rarely rather than
//     continuously.
//
// The complementary liveness gate — never probing a worktree whose agent is
// gone — lives in isPollable (resources.go), because that is where agent
// status and the live tmux window set are known.

const (
	// probeTimeout caps a single measurement. Deliberately generous: a cold
	// LFS repo genuinely can take tens of seconds, and the point is that a
	// ceiling exists at all, not that it is tight.
	probeTimeout = 20 * time.Second

	// probeKillGrace is how long Wait is allowed to linger after the process
	// group has been signalled, before we give up on a clean reap.
	probeKillGrace = 2 * time.Second

	// probeBackoffFactor multiplies the observed duration to get the quiet
	// period that follows it, holding subprocess work to roughly
	// 1/probeBackoffFactor of wall-clock per worktree in the steady state.
	probeBackoffFactor = 10

	// probeMinBackoff keeps a fast measurement from being repeated more often
	// than the refresh tick, which would be pointless work.
	probeMinBackoff = 2 * time.Second

	// probeMaxBackoff bounds the quiet period so a worktree that was slow
	// once (cold cache, a concurrent checkout) still recovers a fresh reading
	// within a reasonable time.
	probeMaxBackoff = 5 * time.Minute
)

type probeState struct {
	// inFlight is the single-flight latch: set while a measurement goroutine
	// for this path is running, cleared when it returns.
	inFlight bool
	// value / haveValue hold the last successful reading. haveValue stays
	// false until the first measurement completes, letting callers tell
	// "measured zero" apart from "not measured yet".
	value     int64
	haveValue bool
	// nextAllowed is the earliest time this path may be measured again.
	nextAllowed time.Time
	// lastDuration is the most recent measurement's wall time, kept for the
	// backoff calculation and for tests.
	lastDuration time.Duration
	// timedOut records whether the last measurement hit probeTimeout.
	timedOut bool
}

// diskProbe is the bounded, self-throttling front end to the per-worktree disk
// measurements described above. It is safe for concurrent use and is held by
// pointer on the TUI model so it survives bubbletea's value-copy of the model.
type diskProbe struct {
	mu     sync.Mutex
	states map[string]*probeState

	// measure and now are injection points for tests; production values are
	// measureDiskUsage and time.Now.
	measure func(path string, incremental bool) (int64, error)
	now     func() time.Time
}

func newDiskProbe() *diskProbe {
	return &diskProbe{
		states:  make(map[string]*probeState),
		measure: measureDiskUsage,
		now:     time.Now,
	}
}

// Sample returns the most recently measured disk usage for path and whether
// any measurement has ever succeeded. It never blocks on a subprocess: when a
// fresh reading is due and none is already running for this path, it launches
// one in the background and returns the previous value.
//
// incremental selects the measurement strategy — git-diff-based for rift
// snapshots, du otherwise. It is a property of the project, so it is stable
// for any given path.
func (p *diskProbe) Sample(path string, incremental bool) (int64, bool) {
	if p == nil || path == "" {
		return 0, false
	}

	p.mu.Lock()
	st := p.states[path]
	if st == nil {
		st = &probeState{}
		p.states[path] = st
	}
	value, haveValue := st.value, st.haveValue
	// Single-flight: a measurement already running for this path means we
	// take the cached value and fork nothing. This is the invariant that
	// turns the old unbounded pile into a constant.
	due := !st.inFlight && !p.now().Before(st.nextAllowed)
	if due {
		st.inFlight = true
	}
	p.mu.Unlock()

	if due {
		go p.refresh(path, incremental)
	}
	return value, haveValue
}

// refresh runs one measurement and records the result plus the backoff it
// earned.
func (p *diskProbe) refresh(path string, incremental bool) {
	start := p.now()
	value, err := p.measure(path, incremental)
	elapsed := p.now().Sub(start)

	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.states[path]
	if st == nil {
		// Retain dropped this path while the measurement was in flight — the
		// agent is gone, so the reading is worthless. Discarding it also
		// keeps us from resurrecting the map entry we just pruned.
		return
	}
	st.inFlight = false
	st.lastDuration = elapsed
	st.timedOut = err == context.DeadlineExceeded
	st.nextAllowed = p.now().Add(backoffFor(elapsed))
	if err == nil {
		st.value = value
		st.haveValue = true
	}
}

// Retain drops per-path state for every worktree not in keep, so the map does
// not grow without bound as agents come and go across a long session. A path
// whose measurement is still in flight keeps its entry until that measurement
// returns; dropping it early would let a concurrent Sample start a second one
// and defeat single-flight.
func (p *diskProbe) Retain(keep map[string]bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for path, st := range p.states {
		if keep[path] || st.inFlight {
			continue
		}
		delete(p.states, path)
	}
}

// backoffFor maps how long a measurement took onto how long we then leave that
// worktree alone. A fast measurement is throttled only to the refresh
// interval; a slow one earns a quiet period an order of magnitude longer than
// its own cost. This is the "back off when calls are slow instead of issuing
// more" property: the slower a worktree is to measure, the less often we touch
// it.
func backoffFor(d time.Duration) time.Duration {
	if d <= 0 {
		return probeMinBackoff
	}
	b := d * probeBackoffFactor
	if b < probeMinBackoff {
		return probeMinBackoff
	}
	if b > probeMaxBackoff {
		return probeMaxBackoff
	}
	return b
}

// measureDiskUsage performs one bounded disk measurement for path.
func measureDiskUsage(path string, incremental bool) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	if incremental {
		return measureIncrementalDiskUsage(ctx, path)
	}
	return measureDuDiskUsage(ctx, path)
}

// measureIncrementalDiskUsage sums the sizes of files that differ from HEAD
// plus untracked files — the only meaningful "disk cost" figure for a rift
// snapshot, whose tracked content is shared with the source repo.
//
// Unlike the code this replaced, a git failure is reported rather than falling
// back to du. That fallback was actively harmful: du on a rift snapshot
// reports the entire source repo (wrong by orders of magnitude), and it does
// so with the same unbounded full-tree walk that caused the overload.
// Reporting the error instead leaves the previous good reading on display.
func measureIncrementalDiskUsage(ctx context.Context, path string) (int64, error) {
	modifiedOut, err := runProbeGit(ctx, path, "diff", "--name-only", "HEAD")
	if err != nil {
		return 0, err
	}
	untrackedOut, err := runProbeGit(ctx, path, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return 0, err
	}

	var totalBytes int64
	seen := make(map[string]bool)
	for _, output := range [][]byte{modifiedOut, untrackedOut} {
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			if info, err := os.Stat(filepath.Join(path, line)); err == nil {
				totalBytes += info.Size()
			}
		}
	}
	return totalBytes, nil
}

// measureDuDiskUsage is the bounded form of the plain-worktree du measurement.
// The flags and unit differ per platform (BSD du has no -b), so the argv comes
// from duArgs in the platform files and the result is scaled by duUnitBytes.
func measureDuDiskUsage(ctx context.Context, path string) (int64, error) {
	argv := duArgs(path)
	out, err := runProbeCommand(ctx, nil, argv[0], argv[1:]...)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 1 {
		return 0, nil
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	return n * duUnitBytes, nil
}

// runProbeGit runs one read-only git command under ctx with the environment a
// background status probe wants.
func runProbeGit(ctx context.Context, path string, args ...string) ([]byte, error) {
	full := append([]string{
		// The fsmonitor hook is a per-repo daemon handshake; a wedged or slow
		// monitor is one more way a read-only probe can hang, and a one-shot
		// call gains nothing from it.
		"-c", "core.fsmonitor=false",
		"-C", path,
	}, args...)

	env := append(os.Environ(),
		// Do not materialise LFS content just to size a pointer file. This is
		// the single biggest per-call cost reduction for the repo that
		// motivated this code (41 committed zarr volumes behind git-LFS).
		"GIT_LFS_SKIP_SMUDGE=1",
		// Read-only: skip the opportunistic index refresh and its write, so
		// the probe never contends with the agent's own git commands for
		// index.lock in the same worktree.
		"GIT_OPTIONAL_LOCKS=0",
		// Never block waiting on a credential or SSH prompt.
		"GIT_TERMINAL_PROMPT=0",
	)

	return runProbeCommand(ctx, env, "git", full...)
}

// runProbeCommand runs one short-lived measurement subprocess in its own
// process group and guarantees the whole group is reaped.
//
// The process group is the load-bearing detail, specifically because of
// git-LFS: `git diff` in an LFS repo spawns `git-lfs filter-process` children
// that survive a signal aimed at git alone. The machine that motivated this
// code had 410 orphaned `git-lfs filter-process` entries. Signalling the
// negated pgid takes the children with it.
func runProbeCommand(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Cancel signals the whole process group rather than just the direct
	// child; WaitDelay guarantees Wait returns even if a grandchild holds the
	// output pipe open, so this goroutine — and its single-flight slot — can
	// never be pinned forever.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = probeKillGrace

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, context.DeadlineExceeded
		}
		return nil, err
	}
	return out, nil
}
