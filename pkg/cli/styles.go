package cli

import (
	"fmt"
	"os"
	"strings"
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
	// Asleep is an app at zero by its own min_scale, woken by its next request:
	// quiet like stopped, since nothing is wrong, and distinct from it, since
	// nothing is needed to bring it back.
	case "asleep":
		return lipgloss.NewStyle().Foreground(colorMuted).Render("◌ " + status)
	case "not_provisioned":
		return lipgloss.NewStyle().Bold(true).Foreground(colorDanger).Render("● " + status)
	case "pending", "provisioning", "invited", "unchecked":
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

// messageBox is a bordered box printed on a line of its own, with one empty
// line above it. The empty line is a real newline and not a lipgloss margin:
// a margin row is padded to the box's width with spaces, which is invisible
// in a terminal and a line of trailing whitespace in anything that captures
// the output.
type messageBox struct{ lipgloss.Style }

func (b messageBox) Render(strs ...string) string {
	return "\n" + b.Style.Render(strs...)
}

// Success message box
var successBox = messageBox{lipgloss.NewStyle().
	BorderStyle(lipgloss.RoundedBorder()).
	BorderForeground(colorSuccess).
	Padding(0, 2)}

// Warning box: something the user has to decide on, not a failure
var warnBox = messageBox{lipgloss.NewStyle().
	BorderStyle(lipgloss.RoundedBorder()).
	BorderForeground(colorWarning).
	Padding(0, 2)}

// Error message box
var errorBox = messageBox{lipgloss.NewStyle().
	BorderStyle(lipgloss.RoundedBorder()).
	BorderForeground(colorDanger).
	Padding(0, 2)}

// Key-value pair rendering for detail views
// renderKeyValue draws one label and its value as one row. A value holding
// several lines keeps the label on its first line, with the rest indented
// under the value: a box row is then as many terminal lines as the value has,
// each one bordered by renderInfoBox.
func renderKeyValue(key, value string) string {
	return renderKeyValueWidth(key, value, labelWidth(key))
}

// labelWidth is the column a label takes: the label itself and one space,
// never narrower than the sixteen columns the boxes have always used, so a
// box with short labels keeps its familiar shape.
func labelWidth(labels ...string) int {
	width := 16
	for _, l := range labels {
		if w := lipgloss.Width(l) + 1; w > width {
			width = w
		}
	}
	return width
}

func renderKeyValueWidth(key, value string, width int) string {
	k := lipgloss.NewStyle().Bold(true).Width(width).Render(key)
	lines := strings.Split(value, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.Repeat(" ", width) + lines[i]
	}
	return k + strings.Join(lines, "\n")
}

// Info box for resource creation results. The label column is sized from the
// longest label in the box, so a label the fixed sixteen columns could not
// hold no longer wraps inside its cell and breaks the border
// .
func renderInfoBox(title string, pairs [][]string) string {
	labels := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		labels = append(labels, pair[0])
	}
	column := labelWidth(labels...)
	rows := make([]string, 0, len(pairs))
	width := 0
	for _, pair := range pairs {
		for _, line := range strings.Split(renderKeyValueWidth(pair[0], pair[1], column), "\n") {
			rows = append(rows, line)
			if w := lipgloss.Width(line); w > width {
				width = w
			}
		}
	}
	// The title sits in the top border rather than on a row of its own, so the
	// box is a table with a caption and not a dialog. One space of padding
	// inside the border; the top rule is as wide as the padded rows.
	label := " " + title + " "
	if w := lipgloss.Width(label) + 1; w > width+2 {
		width = w - 2
	}
	border := lipgloss.NewStyle().Foreground(colorPrimary)
	head := lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render(label)
	fill := width + 2 - 1 - lipgloss.Width(label)
	var lines []string
	lines = append(lines, border.Render("╭─")+head+border.Render(strings.Repeat("─", fill)+"╮"))
	cell := lipgloss.NewStyle().Width(width)
	for _, row := range rows {
		lines = append(lines, border.Render("│ ")+cell.Render(row)+border.Render(" │"))
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", width+2)+"╯"))
	return strings.Join(lines, "\n")
}
