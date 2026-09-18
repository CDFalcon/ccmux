package tui

import (
	"regexp"
	"strings"
	"testing"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// TestRenderLogo_ShouldSpellCodingMultiplexer_GivenStyledFragments guards the
// splash tagline. renderLogo builds it from separately styled fragments, so no
// contiguous "Colby's Coding Multiplexer" literal exists in the source — a
// grep-driven rename sails right past it, which is how the banner kept saying
// "Claude" for two releases after everything else was renamed. Assert on the
// assembled, style-stripped text instead.
func TestRenderLogo_ShouldSpellCodingMultiplexer_GivenStyledFragments(t *testing.T) {
	tagline := ansiEscape.ReplaceAllString(renderLogo(), "")

	if !strings.Contains(tagline, "Colby's Coding Multiplexer") {
		t.Errorf("logo tagline should read \"Colby's Coding Multiplexer\", got:\n%s", tagline)
	}
	if strings.Contains(tagline, "Claude") {
		t.Errorf("logo tagline should not brand ccmux as Claude-only (it also drives Codex), got:\n%s", tagline)
	}
}
