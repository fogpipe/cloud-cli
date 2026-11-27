package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
)

// attentionRow is one line of the attention header: a resource and what is
// wrong with it, or a check that could not be made.
type attentionRow struct {
	// rank orders the header: checks not made first, then faults, then what
	// waits on the tenant.
	rank int
	kind string
	name string
	text string
	// many is how many resources a folded row stands for; zero for one.
	many int
}

const (
	rankUnchecked = iota
	rankFault
	rankWaiting
)

// kindOrder is the order kinds take within one rank.
var kindOrder = map[string]int{"app": 0, "database": 1, "domain": 2, "job": 3, "runner": 4}

// collapseAt is how many resources of one kind sharing one reason become a
// single line, so a fault across a fleet reads as one fault.
const collapseAt = 3

// attentionRows is everything in the document someone has to act on or know
// was not looked at, in the order the console shows it.
func attentionRows(s *client.ProjectStatus) []attentionRow {
	var rows []attentionRow
	names := map[string]map[string]string{"app": {}, "database": {}, "job": {}}
	for _, a := range s.Apps {
		names["app"][a.ID] = a.Name
	}
	for _, d := range s.Databases {
		names["database"][d.ID] = d.Name
	}
	for _, j := range s.Jobs {
		names["job"][j.ID] = j.Name
	}
	for _, u := range s.Unchecked {
		row := attentionRow{rank: rankUnchecked, kind: u.Kind, text: "not checked (" + u.Check + "): " + u.Error}
		if u.Kind != "" {
			row.name = u.ID
			if n, ok := names[u.Kind][u.ID]; ok {
				row.name = n
			}
		}
		rows = append(rows, row)
	}
	add := func(kind, name string, problems []client.StatusProblem) {
		for _, p := range problems {
			rank := rankFault
			if p.Class == client.ProblemWaiting {
				rank = rankWaiting
			}
			rows = append(rows, attentionRow{rank: rank, kind: kind, name: name, text: problemSentence(p)})
		}
	}
	for _, a := range s.Apps {
		add("app", a.Name, a.Problems)
	}
	for _, d := range s.Databases {
		add("database", d.Name, d.Problems)
	}
	for _, d := range s.Domains {
		add("domain", d.Domain, d.Problems)
	}
	for _, j := range s.Jobs {
		add("job", j.Name, j.Problems)
	}
	if s.Runner != nil {
		add("runner", "", s.Runner.Problems)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		// A check over the whole project comes before one about a resource.
		if (a.kind == "") != (b.kind == "") {
			return a.kind == ""
		}
		if kindOrder[a.kind] != kindOrder[b.kind] {
			return kindOrder[a.kind] < kindOrder[b.kind]
		}
		return a.name < b.name
	})
	return collapse(rows)
}

// collapse folds collapseAt or more rows of one kind and one reason into one
// row naming how many there are. Rows stay in their order; the folded row
// takes the place of the first it stands for.
func collapse(rows []attentionRow) []attentionRow {
	type key struct {
		rank       int
		kind, text string
	}
	reasonOf := func(r attentionRow) key {
		reason, _, _ := strings.Cut(r.text, ": ")
		return key{r.rank, r.kind, reason}
	}
	count := map[key]int{}
	for _, r := range rows {
		if r.kind != "" {
			count[reasonOf(r)]++
		}
	}
	out := make([]attentionRow, 0, len(rows))
	done := map[key]bool{}
	for _, r := range rows {
		k := reasonOf(r)
		if r.kind == "" || count[k] < collapseAt {
			out = append(out, r)
			continue
		}
		if done[k] {
			continue
		}
		done[k] = true
		out = append(out, attentionRow{rank: r.rank, kind: r.kind, many: count[k], text: k.text})
	}
	return out
}

// problemSentence is a problem as one line: the reason, what the server said
// about it, and how long ago it was seen.
func problemSentence(p client.StatusProblem) string {
	line := p.Reason
	if p.Detail != "" {
		line += ": " + p.Detail
	}
	if p.Count > 1 {
		line += fmt.Sprintf(" (×%d)", p.Count)
	}
	if at, err := time.Parse(time.RFC3339, p.Since); err == nil {
		line += ", " + humanAge(at) + " ago"
	}
	return line
}

// renderAttention is the header above the tables: what needs someone, or that
// nothing does and what was checked to say so. A document with a check not
// made never reads as clean.
func renderAttention(s *client.ProjectStatus) string {
	rows := attentionRows(s)
	if len(rows) == 0 {
		return lipgloss.NewStyle().Foreground(colorSuccess).Render("✓ Nothing needs attention") +
			mutedStyle.Render(" · checked "+checkedSummary(s)) + "\n\n"
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Needs attention (%d)", len(rows))) + "\n")
	label := func(r attentionRow) string {
		if r.kind == "" {
			return "not checked"
		}
		if r.many > 0 {
			return fmt.Sprintf("%d %ss", r.many, r.kind)
		}
		return strings.TrimSpace(r.kind + " " + r.name)
	}
	width := 0
	for _, r := range rows {
		width = max(width, lipgloss.Width(label(r)))
	}
	for _, r := range rows {
		mark := lipgloss.NewStyle().Foreground(colorInfo).Render("?")
		switch r.rank {
		case rankFault:
			mark = lipgloss.NewStyle().Bold(true).Foreground(colorDanger).Render("✗")
		case rankWaiting:
			mark = lipgloss.NewStyle().Foreground(colorWarning).Render("◐")
		}
		text := r.text
		if r.rank == rankUnchecked && r.kind == "" {
			text = strings.TrimPrefix(text, "not checked ")
		}
		b.WriteString(fmt.Sprintf("  %s %-*s  %s\n", mark, width, label(r), text))
	}
	return b.String() + "\n"
}

// checkedSummary names what the clean verdict covers. Buckets have no checks,
// so they are never in it.
func checkedSummary(s *client.ProjectStatus) string {
	var parts []string
	count := func(n int, one, many string) {
		if n == 1 {
			parts = append(parts, "1 "+one)
		} else if n > 1 {
			parts = append(parts, fmt.Sprintf("%d %s", n, many))
		}
	}
	count(len(s.Apps), "app", "apps")
	count(len(s.Databases), "database", "databases")
	count(len(s.Jobs), "job", "jobs")
	count(len(s.Domains), "host", "hosts")
	if s.Runner != nil {
		parts = append(parts, "the runner")
	}
	switch len(parts) {
	case 0:
		return "nothing: the project holds nothing a check reads"
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}
