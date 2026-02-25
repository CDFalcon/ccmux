package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/CDFalcon/ccmux/internal/version"
)

type ChangelogEntry struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

const repo = "colby-duke-ai/ccmux"

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
	return DownloadUpdateWithProgress(targetVersion, nil)
}

func DownloadUpdateWithProgress(targetVersion string, onProgress func(pct int)) error {
	pattern := fmt.Sprintf("ccmux-%s-%s", runtime.GOOS, runtime.GOARCH)
	expectedSize := getAssetSize(targetVersion, pattern)

	currentBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find current binary: %w", err)
	}

	currentBinary, err = filepath.EvalSymlinks(currentBinary)
	if err != nil {
		return fmt.Errorf("failed to resolve binary path: %w", err)
	}

	tmpDir, err := os.MkdirTemp(filepath.Dir(currentBinary), ".ccmux-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	downloadedFile := filepath.Join(tmpDir, pattern)

	errCh := make(chan error, 1)
	go func() {
		cmd := exec.Command("gh", "release", "download", targetVersion,
			"--repo", repo,
			"--pattern", pattern,
			"--dir", tmpDir,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			errCh <- fmt.Errorf("failed to download update: %s: %w", string(output), err)
		} else {
			errCh <- nil
		}
	}()

	if expectedSize > 0 && onProgress != nil {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case err := <-errCh:
				if err != nil {
					return err
				}
				onProgress(100)
				return installBinary(downloadedFile, currentBinary)
			case <-ticker.C:
				if info, statErr := os.Stat(downloadedFile); statErr == nil {
					pct := int(info.Size() * 100 / expectedSize)
					if pct > 99 {
						pct = 99
					}
					onProgress(pct)
				}
			}
		}
	}

	if err := <-errCh; err != nil {
		return err
	}
	if onProgress != nil {
		onProgress(100)
	}
	return installBinary(downloadedFile, currentBinary)
}

func getAssetSize(targetVersion, pattern string) int64 {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/releases/tags/%s", repo, targetVersion),
		"--jq", fmt.Sprintf(`.assets[] | select(.name == "%s") | .size`, pattern))
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0
	}
	return size
}

func installBinary(src, dst string) error {
	tmpPath := dst + ".tmp"
	if err := copyFile(src, tmpPath); err != nil {
		return fmt.Errorf("failed to stage downloaded file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to set permissions: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to replace binary: %w", err)
	}
	return nil
}

func FetchChangelog(currentVersion, latestVersion string) ([]ChangelogEntry, error) {
	if currentVersion == "dev" || latestVersion == "" {
		return nil, nil
	}

	compareCmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/compare/%s...%s", repo, currentVersion, latestVersion),
		"--jq", "[.commits[].commit.message]")
	compareOutput, err := compareCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to compare versions: %w", err)
	}

	var messages []string
	if err := json.Unmarshal(compareOutput, &messages); err != nil {
		return nil, fmt.Errorf("failed to parse compare output: %w", err)
	}

	includedPRs := make(map[int]bool)
	for _, msg := range messages {
		firstLine := strings.Split(msg, "\n")[0]
		if strings.HasPrefix(firstLine, "Merge pull request #") {
			rest := strings.TrimPrefix(firstLine, "Merge pull request #")
			if idx := strings.Index(rest, " "); idx > 0 {
				if num, err := strconv.Atoi(rest[:idx]); err == nil {
					includedPRs[num] = true
				}
			}
		}
	}

	if len(includedPRs) == 0 {
		return nil, nil
	}

	listCmd := exec.Command("gh", "pr", "list",
		"--repo", repo,
		"--state", "merged",
		"--json", "number,title",
		"--limit", "100")
	listOutput, err := listCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list PRs: %w", err)
	}

	var allPRs []ChangelogEntry
	if err := json.Unmarshal(listOutput, &allPRs); err != nil {
		return nil, fmt.Errorf("failed to parse PR list: %w", err)
	}

	var entries []ChangelogEntry
	for _, pr := range allPRs {
		if includedPRs[pr.Number] {
			entries = append(entries, pr)
		}
	}

	return entries, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
