package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/project"
	"github.com/CDFalcon/ccmux/internal/tmux"
	"github.com/CDFalcon/ccmux/internal/worktree"
	"github.com/spf13/cobra"
)

// This file implements the two operator-facing cleanup commands, `ccmux prune`
// and `ccmux agent-rm`.
//
// They exist because nothing in ccmux ever swept for leftovers. Teardown only
// happened along the live paths (accept / reject / kill / merge), each of which
// could fail silently and leave a worktree on disk; recovery only ran on a cold
// start; and `git worktree prune` was never called at all. The consequence,
// measured on a real machine: 25 `ccmux-*` worktree snapshots on disk for 8 live
// agents, with the leftovers still being polled every 2s because their registry
// entries outlived them.
//
// Both commands are dry-run by default. Both refuse to delete a worktree that
// holds uncommitted or unpushed work, reporting it instead — an agent's worktree
// is the only copy of anything it has not pushed, and a sweep over historical
// leftovers is precisely where that is easiest to destroy by accident.

// pruneReason classifies why a candidate is a leftover, for the report.
type pruneReason string

const (
	reasonNoWindow    pruneReason = "tmux window is gone"
	reasonTerminal    pruneReason = "terminal status"
	reasonNoSession   pruneReason = "tmux session is not running"
	reasonNoAgent     pruneReason = "no agent record references this directory"
	reasonMissingDir  pruneReason = "worktree directory no longer exists"
	reasonFailedSpawn pruneReason = "spawn never completed (no worktree recorded)"
)

// pruneCandidate is one thing prune proposes to remove: a registry entry, a
// directory on disk, or both.
type pruneCandidate struct {
	agentID      string
	sessionID    string
	projectName  string
	worktreePath string
	branchName   string
	tmuxWindow   string
	status       agent.Status
	reason       pruneReason
	// unsaved is filled in during planning so the report and the decision use
	// the same inspection.
	unsaved worktree.UnsavedWork
}

func (c pruneCandidate) label() string {
	if c.agentID != "" {
		return "agent " + c.agentID
	}
	return "orphan " + filepath.Base(c.worktreePath)
}

func pruneCmd() *cobra.Command {
	var apply bool
	var force bool

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove leftover agent records and worktree snapshots",
		Long: `Find and remove leftovers from agents that are no longer running:
registry entries whose tmux window is gone, entries parked in a terminal
status, and ccmux-* worktree directories that no agent record references.

Dry-run by default: prints what it would remove and changes nothing. Pass
--apply to actually remove. A worktree holding uncommitted or unpushed work is
always reported and never removed unless --force is also given.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			candidates, err := planPrune()
			if err != nil {
				return err
			}
			return reportAndMaybeApply(candidates, apply, force, "prune")
		},
	}

	cmd.Flags().BoolVar(&apply, "apply", false, "actually remove (default is a dry run)")
	cmd.Flags().BoolVar(&force, "force", false, "remove even when the worktree holds uncommitted or unpushed work")
	return cmd
}

func agentRmCmd() *cobra.Command {
	var apply bool
	var force bool

	cmd := &cobra.Command{
		Use:   "agent-rm <agent-id>",
		Short: "Remove a single agent: its record, worktree and tmux window",
		Long: `Tear down one agent completely — kill its tmux window, remove its worktree
snapshot and local branch, and delete its registry entry.

Dry-run by default: prints what it would remove and changes nothing. Pass
--apply to actually remove. A worktree holding uncommitted or unpushed work is
reported and never removed unless --force is also given.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			candidate, err := planAgentRm(args[0])
			if err != nil {
				return err
			}
			return reportAndMaybeApply([]pruneCandidate{candidate}, apply, force, "agent-rm")
		},
	}

	cmd.Flags().BoolVar(&apply, "apply", false, "actually remove (default is a dry run)")
	cmd.Flags().BoolVar(&force, "force", false, "remove even when the worktree holds uncommitted or unpushed work")
	return cmd
}

// planAgentRm builds the single candidate for `ccmux agent-rm`. Unlike prune it
// does not require the agent to look stale: the operator named it explicitly.
func planAgentRm(agentID string) (pruneCandidate, error) {
	sessions, err := listSessionIDs()
	if err != nil {
		return pruneCandidate{}, err
	}

	for _, sessionID := range sessions {
		store, err := agent.NewStore(sessionID)
		if err != nil {
			continue
		}
		a, err := store.Get(agentID)
		if err != nil {
			continue
		}
		c := pruneCandidate{
			agentID:      a.ID,
			sessionID:    sessionID,
			projectName:  a.ProjectName,
			worktreePath: a.WorktreePath,
			branchName:   a.BranchName,
			tmuxWindow:   a.TmuxWindow,
			status:       a.Status,
			reason:       "requested explicitly",
			unsaved:      worktree.Inspect(a.WorktreePath),
		}
		return c, nil
	}

	return pruneCandidate{}, fmt.Errorf("agent %s not found in any session", agentID)
}

// planPrune enumerates every leftover across every session, plus orphaned
// directories that no record references.
func planPrune() ([]pruneCandidate, error) {
	sessions, err := listSessionIDs()
	if err != nil {
		return nil, err
	}

	var candidates []pruneCandidate
	// referenced collects every worktree path any surviving record points at, so
	// the directory sweep below does not propose deleting a live agent's tree.
	referenced := make(map[string]bool)

	for _, sessionID := range sessions {
		store, err := agent.NewStore(sessionID)
		if err != nil {
			continue
		}
		agents, err := store.List()
		if err != nil {
			continue
		}

		// A session whose tmux session is not running has no live windows at
		// all; otherwise ask tmux which windows still exist. A nil set means we
		// could not tell, and we then leave window-based judgements alone.
		mgr := tmux.NewManager("ccmux-" + sessionID)
		sessionAlive := mgr.SessionExists()
		var liveWindows map[string]bool
		if sessionAlive {
			liveWindows, _ = mgr.LiveWindowIDs()
		}

		for _, a := range agents {
			reason, stale := prunableReason(a, sessionAlive, liveWindows)
			if !stale {
				if a.WorktreePath != "" {
					referenced[filepath.Clean(a.WorktreePath)] = true
				}
				continue
			}
			candidates = append(candidates, pruneCandidate{
				agentID:      a.ID,
				sessionID:    sessionID,
				projectName:  a.ProjectName,
				worktreePath: a.WorktreePath,
				branchName:   a.BranchName,
				tmuxWindow:   a.TmuxWindow,
				status:       a.Status,
				reason:       reason,
				unsaved:      worktree.Inspect(a.WorktreePath),
			})
		}
	}

	orphans, err := findOrphanedWorktrees(referenced, candidates)
	if err == nil {
		candidates = append(candidates, orphans...)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].agentID != candidates[j].agentID {
			return candidates[i].agentID < candidates[j].agentID
		}
		return candidates[i].worktreePath < candidates[j].worktreePath
	})
	return candidates, nil
}

// prunableReason decides whether a registry entry is a leftover.
func prunableReason(a *agent.Agent, sessionAlive bool, liveWindows map[string]bool) (pruneReason, bool) {
	// A spawn that died before `ccmux register-agent` ran leaves an entry with
	// no worktree and no branch, stuck in StatusSpawning forever.
	if a.WorktreePath == "" && (a.Status == agent.StatusSpawning || a.Status == agent.StatusFailed) {
		return reasonFailedSpawn, true
	}
	if isTerminalStatus(a.Status) {
		return reasonTerminal, true
	}
	if a.WorktreePath != "" && !dirExists(a.WorktreePath) {
		return reasonMissingDir, true
	}
	if !sessionAlive {
		return reasonNoSession, true
	}
	if liveWindows != nil && a.TmuxWindow != "" && !liveWindows[a.TmuxWindow] {
		return reasonNoWindow, true
	}
	return "", false
}

// isTerminalStatus mirrors tui.isTerminal: states an agent cannot leave.
func isTerminalStatus(s agent.Status) bool {
	switch s {
	case agent.StatusMerged, agent.StatusFailed, agent.StatusCleaningUp, agent.StatusKilling:
		return true
	}
	return false
}

// findOrphanedWorktrees looks for `ccmux-*` directories next to each known
// project repo that no agent record references at all. These are the true
// invisibles: a teardown that deleted the record but failed to delete the
// directory left nothing behind that knew the directory existed.
func findOrphanedWorktrees(referenced map[string]bool, planned []pruneCandidate) ([]pruneCandidate, error) {
	projectStore, err := project.NewStore()
	if err != nil {
		return nil, err
	}
	projects, err := projectStore.List()
	if err != nil {
		return nil, err
	}

	// Directories already proposed via a registry entry must not be listed twice.
	plannedPaths := make(map[string]bool)
	for _, c := range planned {
		if c.worktreePath != "" {
			plannedPaths[filepath.Clean(c.worktreePath)] = true
		}
	}

	// Agents create worktrees as <dirname of repo path>/ccmux-<suffix>, so the
	// search space is each project's parent directory. De-duplicate, since
	// several projects commonly live under the same parent.
	parents := make(map[string]bool)
	repoPaths := make(map[string]bool)
	for _, p := range projects {
		repo := p.EffectivePath()
		if repo == "" {
			continue
		}
		repoPaths[filepath.Clean(repo)] = true
		parents[filepath.Dir(filepath.Clean(repo))] = true
	}

	var orphans []pruneCandidate
	for parent := range parents {
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "ccmux-") {
				continue
			}
			path := filepath.Join(parent, e.Name())
			clean := filepath.Clean(path)
			// Never propose a project's own repo, only agent snapshots beside it.
			if repoPaths[clean] || referenced[clean] || plannedPaths[clean] {
				continue
			}
			orphans = append(orphans, pruneCandidate{
				worktreePath: path,
				reason:       reasonNoAgent,
				unsaved:      worktree.Inspect(path),
			})
		}
	}
	return orphans, nil
}

// reportAndMaybeApply prints the plan and, when apply is set, executes it.
func reportAndMaybeApply(candidates []pruneCandidate, apply, force bool, verb string) error {
	if len(candidates) == 0 {
		fmt.Println("Nothing to prune.")
		return nil
	}

	var removable, blocked []pruneCandidate
	for _, c := range candidates {
		if c.unsaved.IsClean() || force {
			removable = append(removable, c)
		} else {
			blocked = append(blocked, c)
		}
	}

	if len(blocked) > 0 {
		fmt.Printf("Skipping %d leftover(s) that hold work:\n", len(blocked))
		for _, c := range blocked {
			fmt.Printf("  ! %s\n", c.label())
			if c.worktreePath != "" {
				fmt.Printf("      %s\n", c.worktreePath)
			}
			fmt.Printf("      %s\n", c.unsaved.Summary())
			if d := c.unsaved.Details(); d != "" {
				fmt.Print(d)
			}
		}
		fmt.Println("    Push or commit this work, or re-run with --force to discard it.")
		fmt.Println()
	}

	if len(removable) == 0 {
		return nil
	}

	action := "Would remove"
	if apply {
		action = "Removing"
	}
	fmt.Printf("%s %d leftover(s):\n", action, len(removable))
	for _, c := range removable {
		fmt.Printf("  - %s (%s)\n", c.label(), c.reason)
		if c.worktreePath != "" {
			fmt.Printf("      worktree: %s\n", c.worktreePath)
		}
		if c.branchName != "" {
			fmt.Printf("      branch:   %s\n", c.branchName)
		}
		if force && !c.unsaved.IsClean() {
			fmt.Printf("      FORCED, discarding: %s\n", c.unsaved.Summary())
		}
	}

	if !apply {
		fmt.Printf("\nDry run — nothing was changed. Re-run with --apply to remove.\n")
		return nil
	}

	var failures int
	for _, c := range removable {
		if err := executePrune(c); err != nil {
			failures++
			fmt.Fprintf(os.Stderr, "  failed to remove %s: %v\n", c.label(), err)
		}
	}
	if failures > 0 {
		return fmt.Errorf("%s completed with %d failure(s)", verb, failures)
	}
	fmt.Printf("\nRemoved %d leftover(s).\n", len(removable))
	return nil
}

// executePrune performs one candidate's teardown: tmux window, worktree
// directory, local branch, launcher scripts, registry entry.
func executePrune(c pruneCandidate) error {
	if c.tmuxWindow != "" && c.sessionID != "" {
		tmux.NewManager("ccmux-" + c.sessionID).KillWindow(c.tmuxWindow)
	}

	if c.worktreePath != "" {
		if err := removeAgentWorktree(&agent.Agent{
			WorktreePath: c.worktreePath,
			BranchName:   c.branchName,
			ProjectName:  c.projectName,
		}); err != nil {
			// Report rather than pressing on to delete the record: leaving the
			// record is what keeps the leftover discoverable next time.
			return err
		}
	}

	if homeDir, err := os.UserHomeDir(); err == nil && c.agentID != "" {
		launcherDir := filepath.Join(homeDir, ".ccmux", "launchers")
		removeLauncherFiles(launcherDir, c.agentID)
	}

	if c.agentID != "" && c.sessionID != "" {
		store, err := agent.NewStore(c.sessionID)
		if err != nil {
			return err
		}
		if err := store.Delete(c.agentID); err != nil && !strings.Contains(err.Error(), "not found") {
			return err
		}
	}

	return nil
}

// listSessionIDs returns every session ccmux has state for, so prune can see
// leftovers from sessions that are no longer running.
func listSessionIDs() ([]string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	sessionsDir := filepath.Join(homeDir, ".ccmux", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}
