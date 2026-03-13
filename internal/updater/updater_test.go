package updater

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdErrorWithStderr_ShouldIncludeStderr_GivenExitError(t *testing.T) {
	// Setup.
	cmd := exec.Command("sh", "-c", "echo 'some error details' >&2; exit 1")
	_, err := cmd.Output()

	// Execute.
	result := cmdErrorWithStderr(err)

	// Assert.
	if !strings.Contains(result, "some error details") {
		t.Errorf("expected stderr in output, got: %s", result)
	}
}

func TestCmdErrorWithStderr_ShouldReturnErrorString_GivenPlainError(t *testing.T) {
	// Setup.
	err := fmt.Errorf("plain error")

	// Execute.
	result := cmdErrorWithStderr(err)

	// Assert.
	if result != "plain error" {
		t.Errorf("expected 'plain error', got: %s", result)
	}
}

func TestNeedsElevation_ShouldReturnFalse_GivenWritableDirectory(t *testing.T) {
	// Setup.
	tmpDir, err := os.MkdirTemp("", "ccmux-updater-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	binaryPath := filepath.Join(tmpDir, "ccmux")

	// Execute.
	result := needsElevation(binaryPath)

	// Assert.
	if result {
		t.Error("expected needsElevation to return false for writable directory")
	}
}

func TestNeedsElevation_ShouldReturnTrue_GivenReadOnlyDirectory(t *testing.T) {
	// Setup.
	tmpDir, err := os.MkdirTemp("", "ccmux-updater-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.Chmod(tmpDir, 0755)
		os.RemoveAll(tmpDir)
	}()

	os.Chmod(tmpDir, 0555)
	binaryPath := filepath.Join(tmpDir, "ccmux")

	// Execute.
	result := needsElevation(binaryPath)

	// Assert.
	if !result {
		t.Error("expected needsElevation to return true for read-only directory")
	}
}

func TestInstallBinary_ShouldInstallSuccessfully_GivenWritableDirectory(t *testing.T) {
	// Setup.
	tmpDir, err := os.MkdirTemp("", "ccmux-updater-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srcFile := filepath.Join(tmpDir, "src-binary")
	if err := os.WriteFile(srcFile, []byte("binary-content"), 0644); err != nil {
		t.Fatal(err)
	}

	dstFile := filepath.Join(tmpDir, "dst-binary")

	// Execute.
	err = installBinary(srcFile, dstFile)

	// Assert.
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	content, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("failed to read installed binary: %v", err)
	}
	if string(content) != "binary-content" {
		t.Errorf("expected 'binary-content', got '%s'", string(content))
	}

	info, err := os.Stat(dstFile)
	if err != nil {
		t.Fatalf("failed to stat installed binary: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("expected permissions 0755, got %v", info.Mode().Perm())
	}
}
