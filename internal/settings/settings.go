package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Settings struct {
	BetaChannel bool `json:"beta_channel"`
}

type Store struct {
	mu       sync.Mutex
	filePath string
}

type storeData struct {
	Version  int      `json:"version"`
	Settings Settings `json:"settings"`
}

const CurrentSchemaVersion = 1

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
		filePath: filepath.Join(ccmuxDir, "settings.json"),
	}, nil
}

func (s *Store) load() (*storeData, error) {
	data := &storeData{
		Version: CurrentSchemaVersion,
	}

	raw, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read settings file: %w", err)
	}

	if err := json.Unmarshal(raw, data); err != nil {
		return nil, fmt.Errorf("failed to parse settings file: %w", err)
	}

	data.Version = CurrentSchemaVersion
	return data, nil
}

func (s *Store) save(data *storeData) error {
	data.Version = CurrentSchemaVersion

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	if err := os.WriteFile(s.filePath, bytes, 0644); err != nil {
		return fmt.Errorf("failed to write settings file: %w", err)
	}

	return nil
}

func (s *Store) Get() (*Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	return &data.Settings, nil
}

func (s *Store) SetBetaChannel(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	data.Settings.BetaChannel = enabled
	return s.save(data)
}
