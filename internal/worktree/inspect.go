package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// inspectTimeout bounds each git call. Inspection runs on the same LFS-heavy
// repos that motivated the polling fix, and it is called from interactive
// operator commands, so it must not hang.
const inspectTimeout = 30 * time.Second

// unsavedWorkCommitLimit caps how many unpushed commit subjects we collect. The
// exact count past a handful does not change the decision (refuse), and the list
// is only there to tell the operator what they would lose.
const unsavedWorkCommitLimit = 20

// UnsavedWork describes work in a worktree that would be destroyed by deleting
// it. It exists so that automated cleanup can refuse to run and report instead:
// an agent's worktree is the only copy of whatever it has not pushed, and a
// prune sweep over historical leftovers is exactly the situation where that is
// easiest to lose by accident.
type UnsavedWork struct {
	// Uncommitted holds `git status --porcelain` lines: modified, staged and
	// untracked paths.
	Uncommitted []string
	// UnpushedCommits holds one-line summaries of commits reachable from HEAD
	// but from no remote-tracking branch.
	UnpushedCommits []string
	// Undetermined is set when we could not establish whether the worktree is
	// safe to delete — a git command failed, or the repo has no remote-tracking
	// refs at all so "unpushed" is unanswerable. Callers must treat this as
	// unsafe: for a destructive operation, "I don't know" and "yes there is
	// work here" deserve the same answer.
	Undetermined string
}

// IsClean reports whether the worktree can be deleted without losing anything.
// It is deliberately conservative: anything we could not determine counts as
// not clean.
func (u UnsavedWork) IsClean() bool {
	return len(u.Uncommitted) == 0 && len(u.UnpushedCommits) == 0 && u.Undetermined == ""
}

// Summary renders a one-line human-readable reason a worktree is not clean, or
// "" when it is.
func (u UnsavedWork) Summary() string {
	if u.IsClean() {
		return ""
	}
	var parts []string
	if n := len(u.Uncommitted); n > 0 {
		parts = append(parts, fmt.Sprintf("%d uncommitted file(s)", n))
	}
	if n := len(u.UnpushedCommits); n > 0 {
		parts = append(parts, fmt.Sprintf("%d unpushed commit(s)", n))
	}
	if u.Undetermined != "" {
		parts = append(parts, "could not verify: "+u.Undetermined)
	}
	return strings.Join(parts, ", ")
}

// Details renders the specifics, indented, for an operator about to decide
// whether to force a deletion.
func (u UnsavedWork) Details() string {
	var b strings.Builder
	for _, line := range u.Uncommitted {
		fmt.Fprintf(&b, "      %s\n", line)
	}
	for _, line := range u.UnpushedCommits {
		fmt.Fprintf(&b, "      commit %s\n", line)
	}
	return b.String()
}

// Inspect reports whether the worktree at path holds work that deleting it
// would destroy.
//
// A path that does not exist is clean (nothing to lose). A path that exists but
// cannot be interrogated is Undetermined, not clean.
func Inspect(path string) UnsavedWork {
	var u UnsavedWork

	if path == "" {
		return UnsavedWork{Undetermined: "empty worktree path"}
	}
	if info, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			// Already gone — there is nothing here to lose.
			return u
		}
		return UnsavedWork{Undetermined: fmt.Sprintf("stat %s: %v", path, err)}
	} else if !info.IsDir() {
		return UnsavedWork{Undetermined: fmt.Sprintf("%s is not a directory", path)}
	}

	status, err := inspectGit(path, "status", "--porcelain")
	if err != nil {
		return UnsavedWork{Undetermined: fmt.Sprintf("git status failed: %v", err)}
	}
	u.Uncommitted = nonEmptyLines(status)

	// "Unpushed" means reachable from HEAD but from no remote-tracking ref. If
	// the repo has no remote-tracking refs at all, that query would report the
	// entire history as unpushed, so refuse to guess instead.
	remotes, err := inspectGit(path, "for-each-ref", "--format=%(refname)", "refs/remotes")
	if err != nil {
		return UnsavedWork{Undetermined: fmt.Sprintf("git for-each-ref failed: %v", err)}
	}
	if len(nonEmptyLines(remotes)) == 0 {
		u.Undetermined = "no remote-tracking refs, cannot tell what has been pushed"
		return u
	}

	unpushed, err := inspectGit(path,
		"log", "--oneline", "--no-decorate",
		fmt.Sprintf("--max-count=%d", unsavedWorkCommitLimit),
		"HEAD", "--not", "--remotes",
	)
	if err != nil {
		// An unborn HEAD (no commits yet) makes `git log` fail; combined with a
		// clean status that genuinely means "nothing here".
		if len(u.Uncommitted) == 0 {
			return u
		}
		return UnsavedWork{
			Uncommitted:  u.Uncommitted,
			Undetermined: fmt.Sprintf("git log failed: %v", err),
		}
	}
	u.UnpushedCommits = nonEmptyLines(unpushed)

	return u
}

func inspectGit(path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), inspectTimeout)
	defer cancel()

	full := append([]string{"-C", path}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(),
		// Same read-only hygiene as the status probe: never materialise LFS
		// content, never take index.lock, never prompt.
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
