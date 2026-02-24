package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	mu       sync.Mutex
	filePath string
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

	raw, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read agents file: %w", err)
	}

	var envelope struct {
		Version int `json:"version"`
	}
	json.Unmarshal(raw, &envelope)

	if envelope.Version < CurrentSchemaVersion {
		raw, err = migrations.Migrate(raw, envelope.Version, CurrentSchemaVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to migrate agents file: %w", err)
		}
	}

	if err := json.Unmarshal(raw, data); err != nil {
		return nil, fmt.Errorf("failed to parse agents file: %w", err)
	}

	data.Version = CurrentSchemaVersion

	return data, nil
}

func (s *Store) save(data *storeData) error {
	data.Version = CurrentSchemaVersion

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
