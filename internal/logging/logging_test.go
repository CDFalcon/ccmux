package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestLog(t *testing.T) (string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "ccmux-logging-test")
	if err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(tmpDir, "test.log")
	var openErr error
	logFile, openErr = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if openErr != nil {
		os.RemoveAll(tmpDir)
		t.Fatal(openErr)
	}

	cleanup := func() {
		Close()
		os.RemoveAll(tmpDir)
	}

	return logPath, cleanup
}

func TestLog_ShouldWriteTimestampedMessage_GivenValidLogFile(t *testing.T) {
	// Setup.
	logPath, cleanup := setupTestLog(t)
	defer cleanup()

	// Execute.
	Log("hello %s", "world")

	// Assert.
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	line := string(content)
	if !strings.Contains(line, "hello world") {
		t.Errorf("expected log to contain 'hello world', got '%s'", line)
	}
	if !strings.HasPrefix(line, "[") {
		t.Errorf("expected log to start with timestamp bracket, got '%s'", line)
	}
}

func TestLog_ShouldNotPanic_GivenNilLogFile(t *testing.T) {
	// Setup.
	origLogFile := logFile
	logFile = nil
	defer func() { logFile = origLogFile }()

	// Execute.
	Log("this should not panic")
}

func TestClose_ShouldNotPanic_GivenNilLogFile(t *testing.T) {
	// Setup.
	origLogFile := logFile
	logFile = nil
	defer func() { logFile = origLogFile }()

	// Execute.
	Close()
}

func TestLog_ShouldWriteMultipleMessages_GivenSequentialCalls(t *testing.T) {
	// Setup.
	logPath, cleanup := setupTestLog(t)
	defer cleanup()

	// Execute.
	Log("first message")
	Log("second message")
	Log("third message")

	// Assert.
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 log lines, got %d", len(lines))
	}
}

func TestLog_ShouldFormatArgs_GivenFormatString(t *testing.T) {
	// Setup.
	logPath, cleanup := setupTestLog(t)
	defer cleanup()

	// Execute.
	Log("count: %d, name: %s", 42, "test")

	// Assert.
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if !strings.Contains(string(content), "count: 42, name: test") {
		t.Errorf("expected formatted message, got '%s'", string(content))
	}
}
