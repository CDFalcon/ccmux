package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CDFalcon/ccmux/internal/agent"
)

// blockingProbe returns a diskProbe whose measurement blocks until release is
// closed, plus counters for how many measurements were started and how many are
// running concurrently at peak.
func blockingProbe(release <-chan struct{}) (*diskProbe, *atomic.Int64, *atomic.Int64) {
	var started, concurrent, peak atomic.Int64
	p := newDiskProbe()
	p.measure = func(path string, incremental bool) (int64, error) {
		started.Add(1)
		cur := concurrent.Add(1)
		for {
			if prev := peak.Load(); cur <= prev || peak.CompareAndSwap(prev, cur) {
				break
			}
		}
		<-release
		concurrent.Add(-1)
		return 42, nil
	}
	return p, &started, &peak
}

// TestDiskProbe_ShouldStartOnlyOneMeasurement_GivenRepeatedSamplesWhileInFlight
// is the core invariant of the overload fix. The TUI's 2s tick re-arms
// unconditionally, so the old code re-issued a worktree's `git diff` /
// `git ls-files` pair on top of the still-running previous pair, forever —
// observed in the wild as 552 concurrent `git diff` processes for a single
// worktree. Single-flight makes the pile impossible: no matter how many times
// the refresh loop asks, one measurement per path is in flight at most.
func TestDiskProbe_ShouldStartOnlyOneMeasurement_GivenRepeatedSamplesWhileInFlight(t *testing.T) {
	// Setup.
	release := make(chan struct{})
	p, started, peak := blockingProbe(release)
	const path = "/tmp/ccmux-probe-single-flight"

	// Execute. Stand in for 500 refresh ticks hitting the same worktree while
	// the first measurement is still running.
	for i := 0; i < 500; i++ {
		p.Sample(path, true)
	}
	waitFor(t, func() bool { return started.Load() >= 1 }, "first measurement to start")

	// Assert. Exactly one measurement was ever started, and never two at once.
	if got := started.Load(); got != 1 {
		t.Errorf("expected exactly 1 measurement for 500 samples of one path, got %d", got)
	}
	if got := peak.Load(); got != 1 {
		t.Errorf("expected peak concurrency 1, got %d", got)
	}

	close(release)
}

// TestDiskProbe_ShouldReturnCachedValue_GivenMeasurementInFlight pins that
// Sample never blocks on the subprocess. A wedged git must not stall the refresh
// that called it, because a stalled refresh is what let the next tick's work
// stack on top of it.
func TestDiskProbe_ShouldReturnCachedValue_GivenMeasurementInFlight(t *testing.T) {
	// Setup. First measurement completes and caches 1234; the second blocks.
	release := make(chan struct{})
	var calls atomic.Int64
	p := newDiskProbe()
	p.measure = func(path string, incremental bool) (int64, error) {
		if calls.Add(1) == 1 {
			return 1234, nil
		}
		<-release
		return 9999, nil
	}
	const path = "/tmp/ccmux-probe-cached"

	p.Sample(path, false)
	waitFor(t, func() bool { return calls.Load() >= 1 }, "first measurement")
	// Clear the backoff so the next Sample is due, then let it block.
	p.mu.Lock()
	p.states[path].nextAllowed = time.Time{}
	p.mu.Unlock()

	// Execute. This Sample starts the blocking measurement and must still
	// return promptly with the previous value.
	done := make(chan int64, 1)
	go func() {
		v, _ := p.Sample(path, false)
		done <- v
	}()

	// Assert.
	select {
	case v := <-done:
		if v != 1234 {
			t.Errorf("expected cached value 1234 while a measurement is in flight, got %d", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Sample blocked on the in-flight measurement; it must never wait on the subprocess")
	}

	close(release)
}

// TestDiskProbe_ShouldBackOffProportionally_GivenSlowMeasurement pins the "back
// off when calls are slow, rather than issuing more" requirement: the quiet
// period after a measurement scales with how expensive that measurement was.
func TestDiskProbe_ShouldBackOffProportionally_GivenSlowMeasurement(t *testing.T) {
	// Setup. A fake clock that only advances while the measurement runs, so a
	// "3 second" measurement costs no real test time.
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	const slow = 3 * time.Second

	var clockMu sync.Mutex
	clock := base

	p := newDiskProbe()
	p.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clock
	}
	p.measure = func(path string, incremental bool) (int64, error) {
		clockMu.Lock()
		clock = clock.Add(slow)
		clockMu.Unlock()
		return 7, nil
	}
	const path = "/tmp/ccmux-probe-backoff"

	// Execute.
	p.Sample(path, true)
	waitFor(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		st := p.states[path]
		return st != nil && !st.inFlight
	}, "measurement to finish")

	// Assert. A 3s measurement earns a 30s quiet period, not another probe on
	// the next 2s tick.
	p.mu.Lock()
	st := p.states[path]
	gotDuration := st.lastDuration
	gotNext := st.nextAllowed
	p.mu.Unlock()

	if gotDuration != slow {
		t.Errorf("expected recorded duration %v, got %v", slow, gotDuration)
	}
	wantBackoff := slow * probeBackoffFactor
	if wantBackoff != 30*time.Second {
		t.Fatalf("test assumption broken: expected 30s backoff, computed %v", wantBackoff)
	}
	if gotNext.Before(base.Add(slow).Add(wantBackoff)) {
		t.Errorf("expected next probe no earlier than %v, got %v",
			base.Add(slow).Add(wantBackoff), gotNext)
	}
}

func TestBackoffFor_ShouldClampToBounds_GivenExtremeDurations(t *testing.T) {
	// Setup / Execute / Assert.
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero uses the floor", 0, probeMinBackoff},
		{"fast probe uses the floor", time.Millisecond, probeMinBackoff},
		{"mid-range scales by the factor", time.Second, 10 * time.Second},
		{"pathological probe hits the ceiling", time.Hour, probeMaxBackoff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := backoffFor(tc.in); got != tc.want {
				t.Errorf("backoffFor(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestDiskProbe_ShouldForgetPath_GivenNotRetained keeps the state map from
// growing once per agent ever run, which is the same "scales with agents ever
// launched" shape as the original bug.
func TestDiskProbe_ShouldForgetPath_GivenNotRetained(t *testing.T) {
	// Setup.
	p := newDiskProbe()
	p.measure = func(path string, incremental bool) (int64, error) { return 1, nil }
	live, stale := "/tmp/ccmux-live", "/tmp/ccmux-stale"
	p.Sample(live, false)
	p.Sample(stale, false)
	waitFor(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.states) == 2 && !p.states[live].inFlight && !p.states[stale].inFlight
	}, "both measurements to finish")

	// Execute.
	p.Retain(map[string]bool{live: true})

	// Assert.
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.states[stale]; ok {
		t.Error("expected stale worktree state to be dropped")
	}
	if _, ok := p.states[live]; !ok {
		t.Error("expected live worktree state to be kept")
	}
}

// TestDiskProbe_ShouldKeepInFlightPath_GivenNotRetained guards a subtle way to
// break single-flight: if Retain dropped a path whose measurement was still
// running, the next Sample would see no state, believe nothing was in flight,
// and start a second measurement.
func TestDiskProbe_ShouldKeepInFlightPath_GivenNotRetained(t *testing.T) {
	// Setup.
	release := make(chan struct{})
	p, started, _ := blockingProbe(release)
	const path = "/tmp/ccmux-probe-inflight-retain"
	p.Sample(path, true)
	waitFor(t, func() bool { return started.Load() == 1 }, "measurement to start")

	// Execute.
	p.Retain(map[string]bool{})
	p.Sample(path, true)

	// Assert.
	if got := started.Load(); got != 1 {
		t.Errorf("expected the in-flight measurement to still be tracked (1 start), got %d", got)
	}
	p.mu.Lock()
	_, ok := p.states[path]
	p.mu.Unlock()
	if !ok {
		t.Error("expected in-flight path to survive Retain")
	}

	close(release)
}

// TestRunProbeCommand_ShouldReapProcessGroup_GivenTimeout is the reaping half of
// the fix. `git diff` in an LFS repo spawns `git-lfs filter-process` children
// that outlive a signal aimed at git alone; the overloaded machine had 410 of
// them orphaned. Killing the negated pgid must take the whole tree with it, and
// must do so within the deadline rather than hanging on the inherited pipe.
func TestRunProbeCommand_ShouldReapProcessGroup_GivenTimeout(t *testing.T) {
	// Setup. A shell that spawns a long-lived child holding stdout open, then
	// waits — the shape of git + a stuck filter process.
	markerDir := t.TempDir()
	marker := filepath.Join(markerDir, "child.pid")
	script := fmt.Sprintf(`sh -c 'echo $$ > %s; sleep 300' & wait`, marker)

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	// Execute.
	start := time.Now()
	_, err := runProbeCommand(ctx, nil, "sh", "-c", script)
	elapsed := time.Since(start)

	// Assert. It returned at the deadline (plus the kill grace), not after
	// sleep 300, and reported the timeout.
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
	if max := 1500*time.Millisecond + probeKillGrace + 3*time.Second; elapsed > max {
		t.Errorf("runProbeCommand took %v, expected it to return by %v", elapsed, max)
	}

	// And the grandchild is gone, not orphaned.
	pidBytes, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Skipf("child never recorded its pid (%v); cannot verify reaping", readErr)
	}
	var childPID int
	if _, err := fmt.Sscanf(string(pidBytes), "%d", &childPID); err != nil || childPID <= 0 {
		t.Skipf("unreadable child pid %q; cannot verify reaping", string(pidBytes))
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if exec.Command("kill", "-0", fmt.Sprint(childPID)).Run() != nil {
			return // gone, as required
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("grandchild pid %d survived the probe timeout; the process group was not reaped", childPID)
}

// TestMeasureIncrementalDiskUsage_ShouldReportError_GivenNonRepo pins the
// deliberate removal of the old du fallback. Falling back was harmful twice
// over: du on a rift snapshot reports the whole source repo (wrong by orders of
// magnitude), and it does it with the same unbounded tree walk that caused the
// overload. Reporting the error keeps the last good reading on display instead.
func TestMeasureIncrementalDiskUsage_ShouldReportError_GivenNonRepo(t *testing.T) {
	// Setup. A directory that is not a git repo.
	dir := t.TempDir()

	// Execute.
	_, err := measureIncrementalDiskUsage(context.Background(), dir)

	// Assert.
	if err == nil {
		t.Error("expected an error for a non-repo path rather than a silent du fallback")
	}
}

// --- liveness gate ---

func TestIsPollable_ShouldReturnTrue_GivenLiveAgentWithExistingWorktree(t *testing.T) {
	// Setup.
	dir := t.TempDir()
	a := &agent.Agent{ID: "a1", WorktreePath: dir, TmuxWindow: "@1", Status: agent.StatusRunning}

	// Execute / Assert.
	if !isPollable(a, map[string]bool{"@1": true}) {
		t.Error("expected a running agent with a live window and existing worktree to be pollable")
	}
}

// TestIsPollable_ShouldReturnFalse_GivenWindowReaped is the exact leak from the
// field report: the PR merged, ccmux reaped the tmux window, but the registry
// entry stayed behind at status ready with its worktree_path intact — so the
// refresh loop kept measuring a directory belonging to an agent that no longer
// existed. 17 of 25 worktrees on the overloaded machine were in this state.
func TestIsPollable_ShouldReturnFalse_GivenWindowReaped(t *testing.T) {
	// Setup. Status still says ready, worktree still on disk, window gone.
	dir := t.TempDir()
	a := &agent.Agent{ID: "a1", WorktreePath: dir, TmuxWindow: "@9", Status: agent.StatusReady}

	// Execute / Assert.
	if isPollable(a, map[string]bool{"@1": true}) {
		t.Error("expected an agent whose tmux window is gone to be skipped")
	}
}

func TestIsPollable_ShouldReturnFalse_GivenTerminalStatus(t *testing.T) {
	// Setup. StatusMerged is where an entry parks when post-merge cleanup fails.
	dir := t.TempDir()
	for _, st := range []agent.Status{
		agent.StatusMerged, agent.StatusFailed, agent.StatusCleaningUp, agent.StatusKilling,
	} {
		t.Run(string(st), func(t *testing.T) {
			a := &agent.Agent{ID: "a1", WorktreePath: dir, TmuxWindow: "@1", Status: st}

			// Execute / Assert.
			if isPollable(a, map[string]bool{"@1": true}) {
				t.Errorf("expected status %s to be skipped", st)
			}
		})
	}
}

func TestIsPollable_ShouldReturnFalse_GivenWorktreeMissingFromDisk(t *testing.T) {
	// Setup.
	a := &agent.Agent{
		ID: "a1", WorktreePath: filepath.Join(t.TempDir(), "gone"),
		TmuxWindow: "@1", Status: agent.StatusRunning,
	}

	// Execute / Assert.
	if isPollable(a, map[string]bool{"@1": true}) {
		t.Error("expected an agent whose worktree no longer exists to be skipped")
	}
}

// TestIsPollable_ShouldFailOpen_GivenUnknownWindowSet pins the safety valve: a
// transient tmux failure must not blank the display by declaring every agent
// dead. nil means "could not enumerate", not "nothing is alive".
func TestIsPollable_ShouldFailOpen_GivenUnknownWindowSet(t *testing.T) {
	// Setup.
	dir := t.TempDir()
	a := &agent.Agent{ID: "a1", WorktreePath: dir, TmuxWindow: "@9", Status: agent.StatusRunning}

	// Execute / Assert.
	if !isPollable(a, nil) {
		t.Error("expected to fail open and keep polling when the live window set is unknown")
	}
}

func TestHasLiveCostData_ShouldReturnTrue_GivenMissingWorktreeButLiveAgent(t *testing.T) {
	// Setup. The session JSONL lives under ~/.claude/projects/, not in the
	// worktree, so cost stays readable after the directory is gone.
	a := &agent.Agent{ID: "a1", WorktreePath: "/tmp/ccmux-deleted", Status: agent.StatusReady}

	// Execute / Assert.
	if !hasLiveCostData(a) {
		t.Error("expected cost data to still be read for a non-terminal agent")
	}
}

func TestHasLiveCostData_ShouldReturnFalse_GivenTerminalAgent(t *testing.T) {
	// Setup. doCleanup already folded this agent's cost into the daily-cost
	// store; re-deriving from JSONL would double-count it.
	a := &agent.Agent{ID: "a1", WorktreePath: "/tmp/ccmux-merged", Status: agent.StatusMerged}

	// Execute / Assert.
	if hasLiveCostData(a) {
		t.Error("expected a merged agent's JSONL to be skipped")
	}
}

// TestQueryAllAgentResources_ShouldNotMeasureStaleWorktrees_GivenManyDeadAgents
// is the end-to-end reproduction of the reported failure, in the shape it was
// measured: 25 worktrees registered, only 8 belonging to a live agent, driven
// through many refresh cycles.
//
// Before the fix, every refresh issued one measurement per worktree with a
// worktree_path — so subprocess count grew as (worktrees x refreshes), 25 x 40 =
// 1000 here, and none of them were cancelled or deduplicated. After the fix,
// the 17 stale worktrees are never measured at all and the 8 live ones are
// single-flighted and backed off.
func TestQueryAllAgentResources_ShouldNotMeasureStaleWorktrees_GivenManyDeadAgents(t *testing.T) {
	// Setup. 8 live agents with live windows, 17 leftovers whose windows were
	// reaped but whose registry entries (and directories) survive.
	const liveCount, staleCount, refreshes = 8, 17, 40

	var agents []*agent.Agent
	liveWindows := make(map[string]bool)
	fastWT := map[string]bool{"mining": true}

	for i := 0; i < liveCount; i++ {
		win := fmt.Sprintf("@live%d", i)
		liveWindows[win] = true
		agents = append(agents, &agent.Agent{
			ID: fmt.Sprintf("live-%d", i), ProjectName: "mining",
			WorktreePath: t.TempDir(), TmuxWindow: win, Status: agent.StatusRunning,
		})
	}
	for i := 0; i < staleCount; i++ {
		agents = append(agents, &agent.Agent{
			ID: fmt.Sprintf("stale-%d", i), ProjectName: "mining",
			WorktreePath: t.TempDir(),
			TmuxWindow:   fmt.Sprintf("@dead%d", i),
			Status:       agent.StatusReady, // exactly what the field report saw
		})
	}

	// A slow measurement — the LFS repo that caused the incident — so that
	// without single-flight every refresh would stack another one.
	var measurements atomic.Int64
	measuredPaths := &sync.Map{}
	release := make(chan struct{})
	probe := newDiskProbe()
	probe.measure = func(path string, incremental bool) (int64, error) {
		measurements.Add(1)
		measuredPaths.Store(path, true)
		<-release
		return 1, nil
	}

	// Execute. Drive the refresh loop repeatedly, as the 2s tick does.
	for i := 0; i < refreshes; i++ {
		queryAllAgentResources(agents, nil, 0, 0, cpuSample{}, fastWT, nil, probe, liveWindows)
	}
	waitFor(t, func() bool { return measurements.Load() >= int64(liveCount) }, "live measurements to start")

	// Assert. One measurement per live worktree, ever — not one per worktree
	// per refresh, and nothing at all for the stale ones.
	if got := measurements.Load(); got != int64(liveCount) {
		t.Errorf("expected exactly %d measurements across %d refreshes of %d worktrees, got %d",
			liveCount, refreshes, len(agents), got)
	}
	for _, a := range agents {
		_, measured := measuredPaths.Load(a.WorktreePath)
		wantMeasured := liveWindows[a.TmuxWindow]
		if measured != wantMeasured {
			t.Errorf("agent %s (window %s): measured=%v, want %v",
				a.ID, a.TmuxWindow, measured, wantMeasured)
		}
	}

	close(release)
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
