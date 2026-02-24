package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/CDFalcon/ccmux/internal/agent"
	"github.com/CDFalcon/ccmux/internal/queue"
)

func TestTruncate_ShouldReturnOriginal_GivenShortString(t *testing.T) {
	// Execute.
	result := truncate("hello", 10)

	// Assert.
	if result != "hello" {
		t.Errorf("expected 'hello', got '%s'", result)
	}
}

func TestTruncate_ShouldTruncateWithEllipsis_GivenLongString(t *testing.T) {
	// Execute.
	result := truncate("this is a very long string", 10)

	// Assert.
	if len(result) != 10 {
		t.Errorf("expected length 10, got %d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Errorf("expected ellipsis suffix, got '%s'", result)
	}
}

func TestTruncate_ShouldReturnExactLength_GivenExactString(t *testing.T) {
	// Execute.
	result := truncate("exactly10!", 10)

	// Assert.
	if result != "exactly10!" {
		t.Errorf("expected 'exactly10!', got '%s'", result)
	}
}

func TestTruncate_ShouldReplaceNewlines_GivenMultilineString(t *testing.T) {
	// Execute.
	result := truncate("line1\nline2", 20)

	// Assert.
	if strings.Contains(result, "\n") {
		t.Errorf("expected newlines to be replaced, got '%s'", result)
	}
	if result != "line1 line2" {
		t.Errorf("expected 'line1 line2', got '%s'", result)
	}
}

func TestMarquee_ShouldReturnOriginal_GivenShortString(t *testing.T) {
	// Execute.
	result := marquee("short", 10, 0)

	// Assert.
	if result != "short" {
		t.Errorf("expected 'short', got '%s'", result)
	}
}

func TestMarquee_ShouldScrollText_GivenLongString(t *testing.T) {
	// Setup.
	longText := "this is a much longer text that exceeds width"

	// Execute.
	result0 := marquee(longText, 20, 0)
	result1 := marquee(longText, 20, MarqueeTickRate)

	// Assert.
	if len([]rune(result0)) != 20 {
		t.Errorf("expected rune length 20, got %d", len([]rune(result0)))
	}
	if result0 == result1 {
		t.Error("expected different output at different offsets")
	}
}

func TestMarquee_ShouldReplaceNewlines_GivenMultilineString(t *testing.T) {
	// Execute.
	result := marquee("line1\nline2\nline3 extra text to exceed", 10, 0)

	// Assert.
	if strings.Contains(result, "\n") {
		t.Errorf("expected newlines to be replaced, got '%s'", result)
	}
}

func TestFormatAge_ShouldReturnJustNow_GivenRecentTimestamp(t *testing.T) {
	// Execute.
	result := formatAge(time.Now())

	// Assert.
	if result != "just now" {
		t.Errorf("expected 'just now', got '%s'", result)
	}
}

func TestFormatAge_ShouldReturnMinutes_GivenMinutesAgo(t *testing.T) {
	// Execute.
	result := formatAge(time.Now().Add(-5 * time.Minute))

	// Assert.
	if !strings.HasSuffix(result, "m ago") {
		t.Errorf("expected minutes format, got '%s'", result)
	}
}

func TestFormatAge_ShouldReturnHours_GivenHoursAgo(t *testing.T) {
	// Execute.
	result := formatAge(time.Now().Add(-3 * time.Hour))

	// Assert.
	if !strings.HasSuffix(result, "h ago") {
		t.Errorf("expected hours format, got '%s'", result)
	}
}

func TestFormatAge_ShouldReturnDays_GivenDaysAgo(t *testing.T) {
	// Execute.
	result := formatAge(time.Now().Add(-48 * time.Hour))

	// Assert.
	if !strings.HasSuffix(result, "d ago") {
		t.Errorf("expected days format, got '%s'", result)
	}
}

func TestSpinner_ShouldCycleThroughFrames_GivenSequentialFrames(t *testing.T) {
	// Execute + Assert.
	for i := 0; i < SpinnerFrameCount; i++ {
		result := spinner(i)
		if result != spinnerFrames[i] {
			t.Errorf("frame %d: expected '%s', got '%s'", i, spinnerFrames[i], result)
		}
	}
}

func TestSpinner_ShouldWrap_GivenFrameBeyondCount(t *testing.T) {
	// Execute.
	result := spinner(SpinnerFrameCount)

	// Assert.
	if result != spinnerFrames[0] {
		t.Errorf("expected first frame on wrap, got '%s'", result)
	}
}

func TestGetItemIcon_ShouldReturnCorrectIcons_GivenItemTypes(t *testing.T) {
	tests := []struct {
		itemType queue.ItemType
		expected string
	}{
		{queue.ItemTypeQuestion, "❓"},
		{queue.ItemTypePRReady, "🔀"},
		{queue.ItemTypeIdle, "💤"},
		{queue.ItemType("unknown"), "•"},
	}

	for _, tt := range tests {
		// Execute.
		result := getItemIcon(tt.itemType)

		// Assert.
		if result != tt.expected {
			t.Errorf("type %s: expected '%s', got '%s'", tt.itemType, tt.expected, result)
		}
	}
}

func TestFilterQueueByType_ShouldFilterCorrectly_GivenMixedItems(t *testing.T) {
	// Setup.
	items := []*queue.QueueItem{
		{ID: "q1", Type: queue.ItemTypeQuestion, AgentID: "a1"},
		{ID: "q2", Type: queue.ItemTypePRReady, AgentID: "a2"},
		{ID: "q3", Type: queue.ItemTypeIdle, AgentID: "a3"},
		{ID: "q4", Type: queue.ItemTypeQuestion, AgentID: "a4"},
	}

	// Execute.
	result := filterQueueByType(items, queue.ItemTypeQuestion, queue.ItemTypeIdle)

	// Assert.
	if len(result) != 3 {
		t.Errorf("expected 3 filtered items, got %d", len(result))
	}
}

func TestFilterQueueByType_ShouldReturnEmpty_GivenNoMatchingItems(t *testing.T) {
	// Setup.
	items := []*queue.QueueItem{
		{ID: "q1", Type: queue.ItemTypePRReady, AgentID: "a1"},
	}

	// Execute.
	result := filterQueueByType(items, queue.ItemTypeQuestion)

	// Assert.
	if len(result) != 0 {
		t.Errorf("expected 0 filtered items, got %d", len(result))
	}
}

func TestFilterQueueByType_ShouldReturnNil_GivenEmptyList(t *testing.T) {
	// Execute.
	result := filterQueueByType(nil, queue.ItemTypeQuestion)

	// Assert.
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestRenderAgentStatus_ShouldReturnStyledText_GivenKnownStatuses(t *testing.T) {
	tests := []struct {
		status   agent.Status
		contains string
	}{
		{agent.StatusRunning, "running"},
		{agent.StatusKilling, "killing"},
		{agent.StatusReady, "ready"},
		{agent.StatusMerged, "merged"},
		{agent.StatusFailed, "failed"},
	}

	for _, tt := range tests {
		// Execute.
		result := renderAgentStatus(tt.status)

		// Assert.
		if !strings.Contains(result, tt.contains) {
			t.Errorf("status %s: expected output to contain '%s', got '%s'", tt.status, tt.contains, result)
		}
	}
}

func TestRenderAgentStatus_ShouldReturnRawString_GivenUnknownStatus(t *testing.T) {
	// Execute.
	result := renderAgentStatus(agent.Status("custom"))

	// Assert.
	if result != "custom" {
		t.Errorf("expected 'custom', got '%s'", result)
	}
}

func TestRenderCtrlCIndicator_ShouldReturnText_GivenPressed(t *testing.T) {
	// Execute.
	result := renderCtrlCIndicator(true)

	// Assert.
	if !strings.Contains(result, "Ctrl+C") {
		t.Errorf("expected Ctrl+C message, got '%s'", result)
	}
}

func TestRenderCtrlCIndicator_ShouldReturnEmpty_GivenNotPressed(t *testing.T) {
	// Execute.
	result := renderCtrlCIndicator(false)

	// Assert.
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestRenderLogo_ShouldReturnMultilineOutput_GivenCall(t *testing.T) {
	// Execute.
	result := renderLogo()

	// Assert.
	lines := strings.Split(result, "\n")
	if len(lines) < 7 {
		t.Errorf("expected at least 7 logo lines, got %d", len(lines))
	}
	if result == "" {
		t.Error("expected non-empty logo output")
	}
	if !strings.Contains(result, "██") {
		t.Error("expected logo to contain block characters")
	}
}

func TestViewStateConstants_ShouldBeSequential(t *testing.T) {
	// Assert.
	if ViewMain != 0 {
		t.Errorf("expected ViewMain to be 0, got %d", ViewMain)
	}
	if ViewSelectProject != 1 {
		t.Errorf("expected ViewSelectProject to be 1, got %d", ViewSelectProject)
	}
	if ViewJumpToAgent != 14 {
		t.Errorf("expected ViewJumpToAgent to be 14, got %d", ViewJumpToAgent)
	}
}

func TestConstants_ShouldHaveExpectedValues(t *testing.T) {
	// Assert.
	if MaxTaskDisplayLen != 40 {
		t.Errorf("expected MaxTaskDisplayLen 40, got %d", MaxTaskDisplayLen)
	}
	if MaxSummaryDisplayLen != 50 {
		t.Errorf("expected MaxSummaryDisplayLen 50, got %d", MaxSummaryDisplayLen)
	}
	if SpinnerFrameCount != 6 {
		t.Errorf("expected SpinnerFrameCount 6, got %d", SpinnerFrameCount)
	}
	if len(spinnerFrames) != SpinnerFrameCount {
		t.Errorf("expected %d spinner frames, got %d", SpinnerFrameCount, len(spinnerFrames))
	}
}
