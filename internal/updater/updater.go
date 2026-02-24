package updater

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/CDFalcon/ccmux/internal/version"
)

const repo = "CDFalcon/ccmux"

func CheckForUpdate() (latestVersion string, hasUpdate bool, err error) {
	cmd := exec.Command("gh", "release", "view", "--repo", repo, "--json", "tagName", "-q", ".tagName")
	output, err := cmd.Output()
	if err != nil {
		return "", false, fmt.Errorf("failed to check for updates: %w", err)
	}

	latest := strings.TrimSpace(string(output))
	if latest == "" {
		return "", false, fmt.Errorf("no releases found")
	}

	if latest == version.Version {
		return latest, false, nil
	}

	return latest, true, nil
}

func DownloadUpdate(targetVersion string) error {
	pattern := fmt.Sprintf("ccmux-%s-%s", runtime.GOOS, runtime.GOARCH)

	currentBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find current binary: %w", err)
	}

	currentBinary, err = filepath.EvalSymlinks(currentBinary)
	if err != nil {
		return fmt.Errorf("failed to resolve binary path: %w", err)
	}

	tmpPath := currentBinary + ".tmp"

	cmd := exec.Command("gh", "release", "download", targetVersion,
		"--repo", repo,
		"--pattern", pattern,
		"--output", tmpPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to download update: %s: %w", string(output), err)
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	if err := os.Rename(tmpPath, currentBinary); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	return nil
}
