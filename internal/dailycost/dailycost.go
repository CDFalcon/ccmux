package dailycost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu       sync.Mutex
	filePath string
}

type storeData struct {
	DailyCosts map[string]float64 `json:"daily_costs"`
}

func NewStore() (*Store, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	dir := filepath.Join(homeDir, ".ccmux")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create ccmux directory: %w", err)
	}

	return &Store{
		filePath: filepath.Join(dir, "daily_costs.json"),
	}, nil
}

func (s *Store) load() (*storeData, error) {
	data := &storeData{
		DailyCosts: make(map[string]float64),
	}

	raw, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read daily costs file: %w", err)
	}

	if err := json.Unmarshal(raw, data); err != nil {
		return nil, fmt.Errorf("failed to parse daily costs file: %w", err)
	}
	if data.DailyCosts == nil {
		data.DailyCosts = make(map[string]float64)
	}

	return data, nil
}

func (s *Store) save(data *storeData) error {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal daily costs: %w", err)
	}

	if err := os.WriteFile(s.filePath, bytes, 0644); err != nil {
		return fmt.Errorf("failed to write daily costs file: %w", err)
	}

	return nil
}

func (s *Store) AddCosts(costs map[string]float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	for date, cost := range costs {
		data.DailyCosts[date] += cost
	}

	return s.save(data)
}

func (s *Store) GetCost(date string) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return 0, err
	}

	return data.DailyCosts[date], nil
}

func (s *Store) GetAllCosts() (map[string]float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	result := make(map[string]float64, len(data.DailyCosts))
	for k, v := range data.DailyCosts {
		result[k] = v
	}
	return result, nil
}
