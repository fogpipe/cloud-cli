package cli

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// fzfPick shows rows in fzf and returns the index of the one chosen, or ok
// false when the user cancelled.
//
// Each row is prefixed with its index in a hidden first column, so a choice is
// resolved by position rather than by re-parsing what was drawn. Rows are
// padded to line their columns up, and padding cannot be removed from a
// rendered line reliably — trimming the line leaves the padding that sits
// before a separator — so a label is not something a value can be recovered
// from. A selection that names no row is an error rather than a cancellation:
// the two are different events and only one of them is the user's doing.
func fzfPick(fzf, prompt, header string, rows []string) (int, bool, error) {
	var input strings.Builder
	for i, row := range rows {
		fmt.Fprintf(&input, "%d\t%s\n", i, row)
	}

	cmd := exec.Command(fzf,
		"--prompt="+prompt,
		"--with-nth=2..",
		"--delimiter=\t",
		"--height=40%",
		"--reverse",
		"--header="+header)
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = nil // fzf draws its UI on the terminal directly

	out, err := cmd.Output()
	if err != nil {
		// Exit code 130 = user cancelled (Esc/Ctrl-C); treat as no selection.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return 0, false, nil
		}
		return 0, false, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return 0, false, nil
	}
	field, _, _ := strings.Cut(line, "\t")
	i, err := strconv.Atoi(field)
	if err != nil || i < 0 || i >= len(rows) {
		return 0, false, fmt.Errorf("the picker returned a selection naming no row: %q", line)
	}
	return i, true, nil
}
