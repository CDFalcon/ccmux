package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGet_ShouldReturnDefaults_GivenNoSettingsFile(t *testing.T) {
	// Setup.
	tmpDir, err := os.MkdirTemp("", "ccmux-settings-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &Store{filePath: filepath.Join(tmpDir, "settings.json")}

	// Execute.
	s, err := store.Get()

	// Assert.
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if s.BetaChannel {
		t.Error("expected BetaChannel to be false by default")
	}
}

func TestSetBetaChannel_ShouldPersistTrue_GivenEnabled(t *testing.T) {
	// Setup.
	tmpDir, err := os.MkdirTemp("", "ccmux-settings-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &Store{filePath: filepath.Join(tmpDir, "settings.json")}

	// Execute.
	err = store.SetBetaChannel(true)

	// Assert.
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	s, err := store.Get()
	if err != nil {
		t.Fatalf("expected no error on Get, got: %v", err)
	}
	if !s.BetaChannel {
		t.Error("expected BetaChannel to be true after enabling")
	}
}

func TestSetBetaChannel_ShouldPersistFalse_GivenDisabledAfterEnabled(t *testing.T) {
	// Setup.
	tmpDir, err := os.MkdirTemp("", "ccmux-settings-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := &Store{filePath: filepath.Join(tmpDir, "settings.json")}
	store.SetBetaChannel(true)

	// Execute.
	err = store.SetBetaChannel(false)

	// Assert.
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	s, err := store.Get()
	if err != nil {
		t.Fatalf("expected no error on Get, got: %v", err)
	}
	if s.BetaChannel {
		t.Error("expected BetaChannel to be false after disabling")
	}
}
