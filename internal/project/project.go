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

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal projects: %w", err)
	}

	if err := os.WriteFile(s.filePath, bytes, 0644); err != nil {
		return fmt.Errorf("failed to write projects file: %w", err)
	}

	return nil
}

func (s *Store) Add(project *Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	absPath, err := filepath.Abs(project.Path)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	project.Path = absPath

	if project.FastWorktreePath != "" {
		absFWP, err := filepath.Abs(project.FastWorktreePath)
		if err != nil {
			return fmt.Errorf("failed to get absolute fast worktree path: %w", err)
		}
		project.FastWorktreePath = absFWP
	}

	if project.UseFastWorktrees && !project.IsSettingUp() {
		effectivePath := project.EffectivePath()
		if !IsProjDirectory(effectivePath) {
			return fmt.Errorf("path is not a proj directory (missing .repo): %s", effectivePath)
		}
	} else if !project.UseFastWorktrees && !isGitRepo(project.Path) {
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

	projects := make([]*Project, 0, len(data.Projects))
	for _, project := range data.Projects {
		projects = append(projects, project)
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})

	return projects, nil
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

	if _, exists := data.Projects[name]; !exists {
		return fmt.Errorf("project %s not found", name)
	}

	delete(data.Projects, name)

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
