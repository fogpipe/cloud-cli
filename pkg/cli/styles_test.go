package cli

import (
	"strings"
	"testing"
)

// Every line of a box is bordered on both sides, whatever the label's length
// and however many lines a value spans.
func TestRenderInfoBoxBordersEveryLine(t *testing.T) {
	box := renderInfoBox("Role viewer", [][]string{
		{"Role", "viewer"},
		{"organization.capacity", "read what the organization holds"},
		{"Access", "user:a@example.com (owner)\nuser:b@example.com (viewer)"},
	})
	lines := strings.Split(box, "\n")
	if want := 2 + 4; len(lines) != want {
		t.Fatalf("box has %d lines, want %d:\n%s", len(lines), want, box)
	}
	for _, l := range lines[1 : len(lines)-1] {
		if !strings.HasPrefix(l, "│") || !strings.HasSuffix(l, "│") {
			t.Errorf("row not bordered: %q", l)
		}
	}
	if !strings.Contains(lines[2], "organization.capacity read what") {
		t.Errorf("long label not on one line with its value: %q", lines[2])
	}
	if !strings.Contains(lines[4], "user:b@example.com") || strings.Contains(lines[4], "Access") {
		t.Errorf("second value line should carry the value alone: %q", lines[4])
	}
}

// A message box's blank line above it is a newline, never a row of spaces:
// lipgloss pads a margin row to the box's width, which captured output shows
// as a line of trailing whitespace.
func TestMessageBoxLeavesNoWhitespaceOnlyLine(t *testing.T) {
	for _, line := range strings.Split(successBox.Render("done"), "\n") {
		if strings.TrimSpace(line) == "" && line != "" {
			t.Fatalf("whitespace-only line %q in %q", line, successBox.Render("done"))
		}
	}
}
