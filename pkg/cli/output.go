package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"sigs.k8s.io/yaml"
)

// outputFormats lists every value accepted by the -o/--output flag.
var outputFormats = []string{"table", "json", "yaml"}

// isValidOutputFormat reports whether format is a supported -o value.
func isValidOutputFormat(format string) bool {
	switch format {
	case "table", "json", "yaml", "yml":
		return true
	}
	return false
}

// isStructured reports whether format is a machine-readable format (json/yaml)
// that serialises a value directly, rather than the human-oriented table view.
func isStructured(format string) bool {
	switch format {
	case "json", "yaml", "yml":
		return true
	}
	return false
}

func renderTable(headers []string, rows [][]string) {
	if len(rows) == 0 {
		fmt.Println(mutedStyle.Render("  No resources found."))
		return
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#555"))).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().
					Bold(true).
					Foreground(colorPrimary).
					Padding(0, 1)
			}
			style := lipgloss.NewStyle().Padding(0, 1)
			// Find STATUS column and style it
			for i, h := range headers {
				if h == "STATUS" && col == i && row >= 0 && row < len(rows) {
					// Don't apply extra style - renderStatus already adds color
					return style
				}
			}
			// Mute ID columns
			if col == 0 && len(headers) > 0 && headers[0] == "ID" {
				return style.Foreground(colorMuted)
			}
			return style
		})

	fmt.Println(t.String())
}

func renderJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// renderYAML marshals v as YAML. It goes through sigs.k8s.io/yaml so the output
// honours the same `json:` struct tags as renderJSON — keys stay identical
// across `-o json` and `-o yaml`.
func renderYAML(v any) error {
	out, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}

// renderData serialises v in whichever structured format the -o flag selects.
// Callers should only reach it when isStructured(<flag>) is true.
func renderData(v any) error {
	if format := rootCmd.Flag("output").Value.String(); format == "yaml" || format == "yml" {
		return renderYAML(v)
	}
	return renderJSON(v)
}

func render(headers []string, rows [][]string, data any) {
	if isStructured(rootCmd.Flag("output").Value.String()) {
		if err := renderData(data); err != nil {
			fmt.Fprintf(os.Stderr, "Error rendering output: %v\n", err)
		}
		return
	}
	renderTable(headers, rows)
}
