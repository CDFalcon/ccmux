package repo

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
		filePath: filepath.Join(ccmuxDir, "repos.json"),
	}, nil
}

func (s *Store) load() (*storeData, error) {
	data := &storeData{
		Repos: make(map[string]*Repo),
	}

	raw, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read repos file: %w", err)
	}

	var envelope struct {
		Version int `json:"version"`
	}
	json.Unmarshal(raw, &envelope)

	if envelope.Version < CurrentSchemaVersion {
		raw, err = migrations.Migrate(raw, envelope.Version, CurrentSchemaVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to migrate repos file: %w", err)
		}
	}

	if err := json.Unmarshal(raw, data); err != nil {
		return nil, fmt.Errorf("failed to parse repos file: %w", err)
	}

	data.Version = CurrentSchemaVersion

	return data, nil
}

func (s *Store) save(data *storeData) error {
	data.Version = CurrentSchemaVersion

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal repos: %w", err)
	}

	if err := os.WriteFile(s.filePath, bytes, 0644); err != nil {
		return fmt.Errorf("failed to write repos file: %w", err)
	}

	return nil
}

func (s *Store) Add(r *Repo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	absPath, err := filepath.Abs(r.Path)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	r.Path = absPath

	if r.FastWorktreePath != "" {
		absFWP, err := filepath.Abs(r.FastWorktreePath)
		if err != nil {
			return fmt.Errorf("failed to get absolute fast worktree path: %w", err)
		}
		r.FastWorktreePath = absFWP
	}

	if r.UseFastWorktrees && !r.IsSettingUp() {
		effectivePath := r.EffectivePath()
		if !IsProjDirectory(effectivePath) {
			return fmt.Errorf("path is not a proj directory (missing .repo): %s", effectivePath)
		}
	} else if !r.UseFastWorktrees && !isGitRepo(r.Path) {
		return fmt.Errorf("path is not a git repository: %s", r.Path)
	}

	data, err := s.load()
	if err != nil {
		return err
	}

	if _, exists := data.Repos[r.Name]; exists {
		return fmt.Errorf("repo with name %s already exists", r.Name)
	}

	data.Repos[r.Name] = r

	return s.save(data)
}

func (s *Store) Get(name string) (*Repo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	r, exists := data.Repos[name]
	if !exists {
		return nil, fmt.Errorf("repo %s not found", name)
	}

	return r, nil
}

func (s *Store) List() ([]*Repo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	repos := make([]*Repo, 0, len(data.Repos))
	for _, r := range data.Repos {
		repos = append(repos, r)
	}

	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Name < repos[j].Name
	})

	return repos, nil
}

func (s *Store) Update(name string, fn func(p *Repo)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	p, exists := data.Repos[name]
	if !exists {
		return fmt.Errorf("repo %s not found", name)
	}

	fn(p)

	if p.UseFastWorktrees && !p.IsSettingUp() {
		effectivePath := p.EffectivePath()
		if !IsProjDirectory(effectivePath) {
			return fmt.Errorf("path is not a proj directory (missing .repo): %s", effectivePath)
		}
	} else if !p.UseFastWorktrees && !isGitRepo(p.Path) {
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

	if _, exists := data.Repos[name]; !exists {
		return fmt.Errorf("repo %s not found", name)
	}

	delete(data.Repos, name)

	return s.save(data)
}

func isGitRepo(path string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = path
	return cmd.Run() == nil
}

func IsProjInstalled() bool {
	_, err := exec.LookPath("proj")
	return err == nil
}

func IsProjDirectory(path string) bool {
	repoDir := filepath.Join(path, ".repo")
	info, err := os.Stat(repoDir)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func FindProjTemplateDir(projDir string) string {
	entries, err := os.ReadDir(projDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "00-") {
			return filepath.Join(projDir, entry.Name())
		}
	}
	return ""
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

func ProjImport(repoPath string, onLine func(string)) (string, error) {
	if !IsProjInstalled() {
		return "", fmt.Errorf("proj is not installed")
	}
	projRoot := os.Getenv("PROJ_ROOT")
	if projRoot == "" {
		return "", fmt.Errorf("PROJ_ROOT is not set — see github.com/Applied-Shared/proj for setup")
	}
	branch := DetectDefaultBranch(repoPath)
	repoName := filepath.Base(repoPath)
	projDir := filepath.Join(projRoot, "projects", repoName)
	cmd := exec.Command("proj", "import", "--local", repoPath, "--branch", branch)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("proj import failed to start: %w", err)
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
		if IsProjDirectory(projDir) && FindProjTemplateDir(projDir) != "" {
			return projDir, nil
		}
		output := strings.Join(lastLines, "\n")
		return "", fmt.Errorf("proj import failed: %w\noutput:\n%s", err, output)
	}
	if !IsProjDirectory(projDir) {
		return "", fmt.Errorf("proj import completed but %s is missing .repo directory", projDir)
	}
	if FindProjTemplateDir(projDir) == "" {
		return "", fmt.Errorf("proj import completed but %s has no template worktree", projDir)
	}
	return projDir, nil
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
