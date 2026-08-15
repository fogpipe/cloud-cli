package cli

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// withSpinner runs action while animating a progress line.
//
// Progress goes to STDERR. It used to go to stdout, where it interleaved with
// whatever the command was actually producing and corrupted every pipe — piping
// any spinner-using command into `jq` failed with "Invalid numeric literal at
// line 1, column 5", the literal being the spinner frame. Decoration is not
// output; stdout belongs to the data.
//
// The animation is also skipped entirely when stderr is not a terminal, so CI
// logs get one line instead of hundreds of carriage-returned frames.
func withSpinner(title string, action func()) {
	done := make(chan struct{})
	var once sync.Once

	go func() {
		action()
		once.Do(func() { close(done) })
	}()

	if !isTerminal(os.Stderr) {
		<-done
		return
	}

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	style := lipgloss.NewStyle().Foreground(colorPrimary)
	i := 0
	for {
		select {
		case <-done:
			fmt.Fprintf(os.Stderr, "\r%s %s\n",
				lipgloss.NewStyle().Foreground(colorSuccess).Render("✓"),
				title)
			return
		default:
			fmt.Fprintf(os.Stderr, "\r%s %s", style.Render(frames[i%len(frames)]), title)
			i++
			time.Sleep(80 * time.Millisecond)
		}
	}
}

// isTerminal reports whether f is an interactive terminal.
//
// The question is answered by asking the file descriptor, not by its mode: a
// character device is not a terminal, and the redirect a script reaches for
// first — `< /dev/null` — is one.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// Color palette
var (
	colorPrimary = lipgloss.Color("#7C3AED") // Purple (brand color)
	colorSuccess = lipgloss.Color("#10B981") // Green
	colorWarning = lipgloss.Color("#F59E0B") // Amber
	colorDanger  = lipgloss.Color("#EF4444") // Red
	colorInfo    = lipgloss.Color("#3B82F6") // Blue
	colorMuted   = lipgloss.Color("#6B7280") // Gray
)

// Pre-rendered status badges (for reference/static use).
var (
	_ = lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("● running")
	_ = lipgloss.NewStyle().Bold(true).Foreground(colorWarning).Render("● deploying")
	_ = lipgloss.NewStyle().Bold(true).Foreground(colorDanger).Render("● failed")
	_ = lipgloss.NewStyle().Foreground(colorMuted).Render("○ stopped")
	_ = lipgloss.NewStyle().Foreground(colorWarning).Render("◌ pending")
	_ = lipgloss.NewStyle().Foreground(colorInfo).Render("◌ provisioning")
)

func renderStatus(status string) string {
	switch status {
	case "running", "active", "succeeded":
		return lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("● " + status)
	case "deploying", "issuing":
		return lipgloss.NewStyle().Bold(true).Foreground(colorWarning).Render("● " + status)
	case "failed":
		return lipgloss.NewStyle().Bold(true).Foreground(colorDanger).Render("● " + status)
	// A scheduled run that never happened, reconstructed from the reaper's audit
	// record (#896). Marked distinctly from `failed` on purpose: nothing failed,
	// which is exactly why a list of `completed` rows with a date quietly absent
	// read as a healthy history during a 32-hour outage.
	case "missed":
		return lipgloss.NewStyle().Bold(true).Foreground(colorDanger).Render("✗ " + status)
	case "stopped":
		return lipgloss.NewStyle().Foreground(colorMuted).Render("○ " + status)
	case "pending", "provisioning":
		return lipgloss.NewStyle().Foreground(colorInfo).Render("◌ " + status)
	default:
		return status
	}
}

// Title/header style
var titleStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(colorPrimary).
	MarginBottom(1)

// Subtle text for IDs and timestamps
var mutedStyle = lipgloss.NewStyle().Foreground(colorMuted)

// Success message box
var successBox = lipgloss.NewStyle().
	BorderStyle(lipgloss.RoundedBorder()).
	BorderForeground(colorSuccess).
	Padding(0, 2).
	MarginTop(1)

// Warning box: something the user has to decide on, not a failure
var warnBox = lipgloss.NewStyle().
	BorderStyle(lipgloss.RoundedBorder()).
	BorderForeground(colorWarning).
	Padding(0, 2).
	MarginTop(1)

// Error message box
var errorBox = lipgloss.NewStyle().
	BorderStyle(lipgloss.RoundedBorder()).
	BorderForeground(colorDanger).
	Padding(0, 2).
	MarginTop(1)

// Key-value pair rendering for detail views
func renderKeyValue(key, value string) string {
	k := lipgloss.NewStyle().Bold(true).Width(16).Render(key)
	return k + value
}

// Info box for resource creation results
func renderInfoBox(title string, pairs [][]string) string {
	header := lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render(title)

	var lines []string
	lines = append(lines, header)
	lines = append(lines, "")
	for _, pair := range pairs {
		lines = append(lines, renderKeyValue(pair[0], pair[1]))
	}

	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(1, 2)

	return box.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}
