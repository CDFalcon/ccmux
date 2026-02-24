// Package agent manages agent state and persistence.
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Status string

const (
	StatusSpawning   Status = "spawning"
	StatusRunning    Status = "running"
	StatusReady      Status = "ready"
	StatusCleaningUp Status = "cleaning_up"
	StatusKilling    Status = "killing"
	StatusMerged     Status = "merged"
	StatusFailed     Status = "failed"
)

type Agent struct {
	ID           string    `json:"id"`
	Task         string    `json:"task"`
	WorktreePath string    `json:"worktree_path"`
	BranchName   string    `json:"branch_name"`
	BaseBranch   string    `json:"base_branch"`
	TmuxWindow   string    `json:"tmux_window"`
	Status       Status    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Store struct {
	mu       sync.Mutex
	filePath string
}

type storeData struct {
	Agents map[string]*Agent `json:"agents"`
}

func NewStore(sessionID string) (*Store, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	sessionDir := filepath.Join(homeDir, ".ccmux", "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	return &Store{
		filePath: filepath.Join(sessionDir, "agents.json"),
	}, nil
}

func (s *Store) load() (*storeData, error) {
	data := &storeData{
		Agents: make(map[string]*Agent),
	}

	bytes, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read agents file: %w", err)
	}

	if err := json.Unmarshal(bytes, data); err != nil {
		return nil, fmt.Errorf("failed to parse agents file: %w", err)
	}

	return data, nil
}

func (s *Store) save(data *storeData) error {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal agents: %w", err)
	}

	if err := os.WriteFile(s.filePath, bytes, 0644); err != nil {
		return fmt.Errorf("failed to write agents file: %w", err)
	}

	return nil
}

func (s *Store) Create(agent *Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	if _, exists := data.Agents[agent.ID]; exists {
		return fmt.Errorf("agent with ID %s already exists", agent.ID)
	}

	now := time.Now()
	agent.CreatedAt = now
	agent.UpdatedAt = now
	data.Agents[agent.ID] = agent

	return s.save(data)
}

func (s *Store) Get(id string) (*Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	agent, exists := data.Agents[id]
	if !exists {
		return nil, fmt.Errorf("agent with ID %s not found", id)
	}

	return agent, nil
}

func (s *Store) List() ([]*Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	agents := make([]*Agent, 0, len(data.Agents))
	for _, agent := range data.Agents {
		agents = append(agents, agent)
	}

	return agents, nil
}

func (s *Store) Update(id string, updateFn func(*Agent)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	agent, exists := data.Agents[id]
	if !exists {
		return fmt.Errorf("agent with ID %s not found", id)
	}

	updateFn(agent)
	agent.UpdatedAt = time.Now()

	return s.save(data)
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	if _, exists := data.Agents[id]; !exists {
		return fmt.Errorf("agent with ID %s not found", id)
	}

	delete(data.Agents, id)

	return s.save(data)
}
