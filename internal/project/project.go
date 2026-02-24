package project

import (
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

	if !isGitRepo(project.Path) {
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
