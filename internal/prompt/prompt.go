package prompt

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
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
		filePath: filepath.Join(ccmuxDir, "prompts.json"),
	}, nil
}

func (s *Store) load() (*storeData, error) {
	data := &storeData{
		Prompts: make(map[string]*Prompt),
	}

	raw, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read prompts file: %w", err)
	}

	if err := json.Unmarshal(raw, data); err != nil {
		return nil, fmt.Errorf("failed to parse prompts file: %w", err)
	}

	data.Version = CurrentSchemaVersion

	return data, nil
}

func (s *Store) save(data *storeData) error {
	data.Version = CurrentSchemaVersion

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal prompts: %w", err)
	}

	if err := os.WriteFile(s.filePath, bytes, 0644); err != nil {
		return fmt.Errorf("failed to write prompts file: %w", err)
	}

	return nil
}

func (s *Store) Add(p *Prompt) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p.ID == "" {
		p.ID = generateID()
	}
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now

	data, err := s.load()
	if err != nil {
		return err
	}

	data.Prompts[p.ID] = p

	return s.save(data)
}

func (s *Store) Get(id string) (*Prompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	p, exists := data.Prompts[id]
	if !exists {
		return nil, fmt.Errorf("prompt %s not found", id)
	}

	return p, nil
}

func (s *Store) List() ([]*Prompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	prompts := make([]*Prompt, 0, len(data.Prompts))
	for _, p := range data.Prompts {
		prompts = append(prompts, p)
	}

	sort.Slice(prompts, func(i, j int) bool {
		return prompts[i].Name < prompts[j].Name
	})

	return prompts, nil
}

func (s *Store) Update(id string, fn func(p *Prompt)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	p, exists := data.Prompts[id]
	if !exists {
		return fmt.Errorf("prompt %s not found", id)
	}

	fn(p)
	p.UpdatedAt = time.Now()

	return s.save(data)
}

func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	if _, exists := data.Prompts[id]; !exists {
		return fmt.Errorf("prompt %s not found", id)
	}

	delete(data.Prompts, id)

	return s.save(data)
}

func generateID() string {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return ""
	}
	return hex.EncodeToString(bytes)
}
