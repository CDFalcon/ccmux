package updater

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

func TestExtractPRNumbers_ShouldExtractTrailingPRRef_GivenSquashMergeCommits(t *testing.T) {
	// Setup.
	messages := []string{
		"Add macOS support for CPU/RAM/disk usage stats (#169)",
		"Resume agent on auto-merge conflict instead of bailing out (#168)",
	}

	// Execute.
	got := extractPRNumbers(messages)

	// Assert.
	want := []int{168, 169}
	if !equalIntSet(got, want) {
		t.Errorf("expected %v, got %v", want, sortedKeys(got))
	}
}

func TestExtractPRNumbers_ShouldExtractPRRef_GivenMergeCommits(t *testing.T) {
	// Setup.
	messages := []string{
		"Merge pull request #161 from colby-duke-ai/colby/fix-review-resume-loop\n\nPrevent review comment triggers from looping agents indefinitely",
		"Merge pull request #160 from colby-duke-ai/colby/dedup-ci-failure-notifications",
	}

	// Execute.
	got := extractPRNumbers(messages)

	// Assert.
	want := []int{160, 161}
	if !equalIntSet(got, want) {
		t.Errorf("expected %v, got %v", want, sortedKeys(got))
	}
}

func TestExtractPRNumbers_ShouldHandleBothStyles_GivenMixedCommits(t *testing.T) {
	// Setup.
	messages := []string{
		"Add macOS support for CPU/RAM/disk usage stats (#169)",
		"Merge pull request #161 from colby-duke-ai/colby/fix-review-resume-loop",
		"Throttle agent CI resume after 3 failures in 15 minutes (#159)",
	}

	// Execute.
	got := extractPRNumbers(messages)

	// Assert.
	want := []int{159, 161, 169}
	if !equalIntSet(got, want) {
		t.Errorf("expected %v, got %v", want, sortedKeys(got))
	}
}

func TestExtractPRNumbers_ShouldIgnoreStrayIssueRefs_GivenIssueReferenceInBody(t *testing.T) {
	// Setup.
	messages := []string{
		"Some refactor without a PR ref\n\nRelated to issue #42 but not a PR merge.",
		"Reference issue #42 in the middle of subject",
	}

	// Execute.
	got := extractPRNumbers(messages)

	// Assert.
	if len(got) != 0 {
		t.Errorf("expected no PRs, got %v", sortedKeys(got))
	}
}

func TestExtractPRNumbers_ShouldDeduplicate_GivenRepeatedPRNumbers(t *testing.T) {
	// Setup.
	messages := []string{
		"Add thing (#100)",
		"Merge pull request #100 from foo/bar",
	}

	// Execute.
	got := extractPRNumbers(messages)

	// Assert.
	want := []int{100}
	if !equalIntSet(got, want) {
		t.Errorf("expected %v, got %v", want, sortedKeys(got))
	}
}

func equalIntSet(got map[int]bool, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for _, n := range want {
		if !got[n] {
			return false
		}
	}
	return true
}

func sortedKeys(m map[int]bool) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
