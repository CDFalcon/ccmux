package project

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Store struct {
	mu       sync.Mutex
	filePath string
}

func NewStore() (*Store, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	ccmuxDir := filepath.Join(homeDir, ".ccmux")
	if err := os.MkdirAll(ccmuxDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create ccmux directory: %w", err)
	}

	return &Store{
		filePath: filepath.Join(ccmuxDir, "projects.json"),
	}, nil
}

func (s *Store) load() (*storeData, error) {
	data := &storeData{
		Projects: make(map[string]*Project),
	}

	raw, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read projects file: %w", err)
	}

	var envelope struct {
		Version int `json:"version"`
	}
	json.Unmarshal(raw, &envelope)

	if envelope.Version < CurrentSchemaVersion {
		raw, err = migrations.Migrate(raw, envelope.Version, CurrentSchemaVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to migrate projects file: %w", err)
		}
	}

	if err := json.Unmarshal(raw, data); err != nil {
		return nil, fmt.Errorf("failed to parse projects file: %w", err)
	}

	data.Version = CurrentSchemaVersion

	return data, nil
}

func (s *Store) save(data *storeData) error {
	data.Version = CurrentSchemaVersion
	data.Order = reconcileOrder(data.Order, data.Projects)

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal projects: %w", err)
	}

	if err := os.WriteFile(s.filePath, bytes, 0644); err != nil {
		return fmt.Errorf("failed to write projects file: %w", err)
	}

	return nil
}

// reconcileOrder ensures Order contains exactly the keys of projects:
// it strips entries for projects that have been removed and appends any
// projects (alphabetically) that are missing from Order. This keeps the
// persisted order self-consistent even if the file was edited by hand.
func reconcileOrder(order []string, projects map[string]*Project) []string {
	known := make(map[string]bool, len(projects))
	for name := range projects {
		known[name] = true
	}

	seen := make(map[string]bool, len(order))
	result := make([]string, 0, len(projects))
	for _, name := range order {
		if !known[name] || seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}

	var missing []string
	for name := range projects {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	result = append(result, missing...)
	return result
}

func (s *Store) Add(project *Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	absPath, err := filepath.Abs(project.Path)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	project.Path = absPath

	// Both standard worktrees and rift-backed fast worktrees require the
	// path to be a real git repo. Rift adds a `.rift` marker on top via
	// `rift init`, but that runs as a separate setup step after Add;
	// during initial registration we only insist on git so the user can
	// opt in to fast worktrees later without having to re-add the project.
	if !project.IsSettingUp() && !isGitRepo(project.Path) {
		return fmt.Errorf("path is not a git repository: %s", project.Path)
	}

	data, err := s.load()
	if err != nil {
		return err
	}

	if _, exists := data.Projects[project.Name]; exists {
		return fmt.Errorf("project with name %s already exists", project.Name)
	}

	data.Projects[project.Name] = project
	data.Order = append(data.Order, project.Name)

	return s.save(data)
}

func (s *Store) Get(name string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	project, exists := data.Projects[name]
	if !exists {
		return nil, fmt.Errorf("project %s not found", name)
	}

	return project, nil
}

func (s *Store) List() ([]*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	order := reconcileOrder(data.Order, data.Projects)

	projects := make([]*Project, 0, len(data.Projects))
	for _, name := range order {
		if p, ok := data.Projects[name]; ok {
			projects = append(projects, p)
		}
	}

	return projects, nil
}

// Move shifts the project named `name` one slot up (delta=-1) or down
// (delta=+1) in the persisted display order. Movement past either end is
// a no-op so callers can blindly bind the action to arrow keys.
func (s *Store) Move(name string, delta int) error {
	if delta != -1 && delta != 1 {
		return fmt.Errorf("Move delta must be -1 or 1, got %d", delta)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	if _, exists := data.Projects[name]; !exists {
		return fmt.Errorf("project %s not found", name)
	}

	order := reconcileOrder(data.Order, data.Projects)
	idx := -1
	for i, n := range order {
		if n == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("project %s not found in order", name)
	}

	target := idx + delta
	if target < 0 || target >= len(order) {
		// Already at the boundary; nothing to do.
		return nil
	}

	order[idx], order[target] = order[target], order[idx]
	data.Order = order

	return s.save(data)
}

func (s *Store) Update(name string, fn func(p *Project)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	p, exists := data.Projects[name]
	if !exists {
		return fmt.Errorf("project %s not found", name)
	}

	fn(p)

	// See Add for why the validation is just "is git repo" regardless of
	// UseFastWorktrees: rift init runs as a separate setup step.
	if !p.IsSettingUp() && !isGitRepo(p.Path) {
		return fmt.Errorf("path is not a git repository: %s", p.Path)
	}

	return s.save(data)
}

func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	if _, exists := data.Projects[name]; !exists {
		return fmt.Errorf("project %s not found", name)
	}

	delete(data.Projects, name)
	// reconcileOrder in save() will strip the removed name, but doing it
	// here keeps the in-memory data consistent if a caller inspects it.
	for i, n := range data.Order {
		if n == name {
			data.Order = append(data.Order[:i], data.Order[i+1:]...)
			break
		}
	}

	return s.save(data)
}

func isGitRepo(path string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = path
	return cmd.Run() == nil
}

// IsRiftInstalled reports whether the `rift` CLI is on PATH. ccmux uses
// rift (https://github.com/anomalyco/rift) to create copy-on-write
// snapshots of project repos for fast worktree creation.
func IsRiftInstalled() bool {
	_, err := exec.LookPath("rift")
	return err == nil
}

// IsRiftInitialized reports whether the given directory has been set up as
// a rift root (i.e. `rift init` has been run on it). Rift marks every
// managed root with a `.rift` file/directory at the top level.
func IsRiftInitialized(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".rift"))
	return err == nil
}

func DetectDefaultBranch(repoPath string) string {
	for _, branch := range []string{"master", "main"} {
		cmd := exec.Command("git", "rev-parse", "--verify", branch)
		cmd.Dir = repoPath
		if cmd.Run() == nil {
			return branch
		}
	}
	return "master"
}

// RiftInit runs `rift init --here` on the given repo path so subsequent
// `rift create` calls can snapshot it. Setup output is streamed line-by-line
// via onLine for the TUI's progress view. If the directory is already
// registered as a rift root, this is a near-instant no-op and still returns
// nil — rift init is idempotent.
//
// The function returns the (unchanged) repo path on success. The path is
// returned rather than discarded so the TUI's setup machinery, which was
// originally built around proj's separate-root model, can keep its current
// shape. With rift, the registered root IS the repo path.
func RiftInit(repoPath string, onLine func(string)) (string, error) {
	if !IsRiftInstalled() {
		return "", fmt.Errorf("rift is not installed (https://github.com/anomalyco/rift)")
	}

	// `--here` pins init to exactly this directory. Without it, rift may
	// walk upward looking for an existing root and register a parent we
	// didn't mean to manage.
	cmd := exec.Command("rift", "init", "--here")
	cmd.Dir = repoPath

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("rift init failed to start: %w", err)
	}

	var lastLines []string
	const maxLastLines = 10
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if onLine != nil {
			onLine(line)
		}
		lastLines = append(lastLines, line)
		if len(lastLines) > maxLastLines {
			lastLines = lastLines[1:]
		}
	}

	if err := cmd.Wait(); err != nil {
		// If the marker is present despite a non-zero exit, treat it as
		// success — rift sometimes warns (e.g. "already initialized") on
		// re-runs with a non-zero status we don't want to surface.
		if IsRiftInitialized(repoPath) {
			return repoPath, nil
		}
		output := strings.Join(lastLines, "\n")
		return "", fmt.Errorf("rift init failed: %w\noutput:\n%s", err, output)
	}
	if !IsRiftInitialized(repoPath) {
		return "", fmt.Errorf("rift init completed but %s has no .rift marker", repoPath)
	}
	return repoPath, nil
}

func GetRepoRoot(path string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Dir = path
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %s", path)
	}
	gitDir := strings.TrimSpace(string(output))
	return filepath.Dir(gitDir), nil
}
