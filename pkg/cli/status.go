package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// minWatchInterval bounds how often --watch may ask. A 304 saves the document,
// not the work behind it: the API re-derives the whole project from the cluster
// on every request, conditional or not. The floor is what stops a watch loop
// from turning one terminal into sustained apiserver load.
const minWatchInterval = time.Second

var projectStatusCmd = &cobra.Command{
	Use:   "status [name]",
	Short: "Show everything the project is running",
	Long: `Show every resource in the project with its live status and problems.

One call, one view: apps, databases, scheduled jobs, custom domains, buckets and
CI runner pools, each with what the platform can actually see about it right now
— replicas ready, crash loops, routes that cannot reach their backend, hostnames
whose certificate never issued, backups that stopped producing restore points.

  fpcloud project status                 the current project
  fpcloud project status my-project      another one
  fpcloud project status --watch         redraw as it changes (every 2s)
  fpcloud project status -o json         the whole document, for a script

Checks that could not run are listed rather than dropped: a report is only
healthy if it also says that everything was looked at.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		project := ""
		if len(args) > 0 {
			project = args[0]
		} else {
			p, err := requireProject()
			if err != nil {
				return err
			}
			project = p
		}

		watch, _ := cmd.Flags().GetBool("watch")
		interval, _ := cmd.Flags().GetDuration("interval")
		if interval < minWatchInterval {
			interval = minWatchInterval
		}

		c := getClient()
		if !watch {
			status, _, err := c.ProjectStatus(context.Background(), project, "")
			if err != nil {
				return err
			}
			if isStructured(rootCmd.Flag("output").Value.String()) {
				return renderData(status)
			}
			fmt.Print(renderProjectStatus(status, nil))
			return nil
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return fmt.Errorf("--watch renders a live view; it cannot be combined with -o %s", rootCmd.Flag("output").Value.String())
		}
		return watchProjectStatus(project, interval)
	},
}

// watchProjectStatus redraws the status view in place until interrupted.
//
// In process rather than under watch(1): re-running the binary each tick would
// re-read the config and re-resolve the project every time, and — because each
// run starts blank — could never say what changed between two of them, which is
// the only thing a watcher is actually looking for.
//
// The client is rebuilt every poll rather than captured once. A Google ID token
// lasts about an hour, so a watch held open longer than that used to fail with
// "invalid or expired credentials" and then stay failed forever: the expired
// token was pinned in memory, and even logging in again — which writes a fresh
// one to disk — could not reach it. Rebuilding per tick both picks up that new
// token and lets `currentIDToken` do the refresh it already knows how to do,
// silently, from the refresh token.
func watchProjectStatus(project string, interval time.Duration) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Alternate screen: the view owns the terminal while it runs and gives back
	// exactly what was there before when it exits.
	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer fmt.Print("\x1b[?25h\x1b[?1049l")

	var (
		etag string
		prev *client.ProjectStatus
	)
	for {
		status, newETag, err := getClient().ProjectStatus(ctx, project, etag)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			// A failed poll is reported in place and retried: a watch that exited
			// on the first blip would be useless during exactly the incident
			// someone opened it for.
			fmt.Print("\x1b[H\x1b[2J" + errorBox.Render(pollFailure(err)) + "\n")
		case status != nil:
			fmt.Print("\x1b[H\x1b[2J" + renderProjectStatus(status, prev) +
				mutedStyle.Render(fmt.Sprintf("\nwatching every %s — ctrl-c to stop\n", interval)))
			prev, etag = status, newETag
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

// statusRow is one resource line, the problems hanging under it, and any
// in-progress activity. Hints are not problems and must not read as one — a
// rollout is the platform working, not the platform broken.
type statusRow struct {
	cells   []string
	notes   []string
	hints   []string
	changed bool
}

// renderCeiling is the org's ceiling and what is spent against it, as one line.
//
// Spend and ceiling are shown together and named as the organization's, because
// the number is shared with every other project it owns: shown alone it reads as
// this project's budget, which is what the per-project caps this replaced
// actually were.
func renderCeiling(p client.StatusProject) string {
	if p.CeilingScope == "" {
		return ""
	}
	axes := []string{}
	if p.MaxCPU != "" {
		axes = append(axes, firstNonEmpty(p.UsedCPU, "?")+"/"+p.MaxCPU+" cpu")
	}
	if p.MaxMemory != "" {
		axes = append(axes, firstNonEmpty(p.UsedMemory, "?")+"/"+p.MaxMemory+" mem")
	}
	if p.MaxPods > 0 {
		axes = append(axes, fmt.Sprintf("%d/%d pods", p.UsedPods, p.MaxPods))
	}
	if len(axes) == 0 {
		return ""
	}
	return p.CeilingScope + " " + strings.Join(axes, " / ")
}

// renderProjectStatus renders the whole document. prev, when non-nil, is the
// previous observation: rows that differ from it are marked, so a watcher sees
// what moved rather than re-reading the whole screen.
func renderProjectStatus(s *client.ProjectStatus, prev *client.ProjectStatus) string {
	var b strings.Builder

	header := lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render(s.Project.Name)
	meta := []string{"ns " + s.Project.Namespace, "egress " + s.Project.Egress}
	if ceiling := renderCeiling(s.Project); ceiling != "" {
		meta = append(meta, ceiling)
	}
	b.WriteString(header + "  " + mutedStyle.Render(strings.Join(meta, "   ")) + "\n")
	if !s.ObservedAt.IsZero() {
		b.WriteString(mutedStyle.Render("observed "+s.ObservedAt.Local().Format("15:04:05")) + "\n")
	}

	prevApps := map[string]client.AppStatus{}
	if prev != nil {
		for _, a := range prev.Apps {
			prevApps[a.Name] = a
		}
	}
	appRows := make([]statusRow, 0, len(s.Apps))
	for _, a := range s.Apps {
		was, seen := prevApps[a.Name]
		hints := []string{}
		if a.Rollout != nil {
			hints = append(hints, "rolling out — "+a.Rollout.Reason)
		}
		if moving := podMovement(a.Pods); moving != "" {
			hints = append(hints, moving)
		}
		appRows = append(appRows, statusRow{
			cells: []string{a.Name, a.Mode, appReadiness(a), releaseLabel(a), shortImage(runningImage(a)), appAge(a), configLabel(a.Config)},
			notes: problemNotes(a.Problems),
			hints: hints,
			// A rollout advancing is the main thing a watcher is waiting on, so
			// its every step counts as a change — including the step from
			// rolling to settled, which no other field moves for.
			changed: seen && (was.Status != a.Status || was.Ready != a.Ready ||
				was.Desired != a.Desired || runningImage(was) != runningImage(a) ||
				was.RunningRelease != a.RunningRelease ||
				rolloutKey(was.Rollout) != rolloutKey(a.Rollout) ||
				podKey(was.Pods) != podKey(a.Pods) ||
				len(was.Problems) != len(a.Problems)),
		})
	}
	b.WriteString(renderStatusSection("APPS", []string{"MODE", "STATUS", "RELEASE", "IMAGE", "AGE", "CONFIG"}, appRows))

	dbRows := make([]statusRow, 0, len(s.Databases))
	for _, d := range s.Databases {
		engine := d.Engine
		if d.Version != "" {
			engine += " " + d.Version
		}
		pooler := "—"
		if d.Pooler {
			pooler = "yes"
		}
		dbRows = append(dbRows, statusRow{
			cells: []string{d.Name, engine, renderStatus(d.Status), pooler},
			notes: problemNotes(d.Problems),
		})
	}
	b.WriteString(renderStatusSection("DATABASES", []string{"ENGINE", "STATUS", "POOLER"}, dbRows))

	jobRows := make([]statusRow, 0, len(s.Jobs))
	for _, j := range s.Jobs {
		schedule := j.Schedule
		if j.Suspended {
			schedule = mutedStyle.Render(j.Schedule + " (suspended)")
		}
		jobRows = append(jobRows, statusRow{
			cells: []string{j.Name, schedule, j.Target, lastRunLabel(j.LastRun)},
		})
	}
	b.WriteString(renderStatusSection("JOBS", []string{"SCHEDULE", "TARGET", "LAST RUN"}, jobRows))

	domainRows := make([]statusRow, 0, len(s.Domains))
	for _, d := range s.Domains {
		owner := d.Owner
		if owner != "" && d.OwnerKind != "" {
			owner = d.OwnerKind + "/" + d.Owner
		}
		mode := d.Mode
		if mode == "" {
			mode = mutedStyle.Render(d.Source)
		}
		domainRows = append(domainRows, statusRow{
			cells: []string{d.Domain, mode, renderStatus(d.Status), tlsLabel(d.TLSStatus), owner},
			notes: problemNotes(d.Problems),
		})
	}
	b.WriteString(renderStatusSection("DOMAINS", []string{"MODE", "STATUS", "TLS", "SERVES"}, domainRows))

	bucketRows := make([]statusRow, 0, len(s.Buckets))
	for _, bk := range s.Buckets {
		website := "—"
		if bk.WebsiteEnabled {
			website = bk.WebsiteURL
			if website == "" {
				website = "enabled"
			}
		}
		used := "—"
		if bk.UsedBytes != nil {
			used = humanizeSize(*bk.UsedBytes)
		}
		bucketRows = append(bucketRows, statusRow{
			cells: []string{bk.Name, used, renderStatus(bk.Status), website},
		})
	}
	b.WriteString(renderStatusSection("BUCKETS", []string{"USED", "STATUS", "WEBSITE"}, bucketRows))

	runnerRows := make([]statusRow, 0, len(s.Runners))
	for _, r := range s.Runners {
		notes := []string{}
		if r.Message != "" {
			notes = append(notes, r.Message)
		}
		runnerRows = append(runnerRows, statusRow{
			cells: []string{r.Name, renderStatus(r.Status),
				fmt.Sprintf("%d (%d–%d)", r.CurrentRunners, r.MinRunners, r.MaxRunners)},
			notes: notes,
		})
	}
	b.WriteString(renderStatusSection("RUNNERS", []string{"STATUS", "ACTIVE"}, runnerRows))

	if len(s.Unchecked) > 0 {
		lines := []string{lipgloss.NewStyle().Bold(true).Render("Not checked — this report is incomplete:")}
		for _, u := range s.Unchecked {
			lines = append(lines, "  "+u.Check+": "+u.Error)
		}
		b.WriteString(warnBox.Render(strings.Join(lines, "\n")) + "\n")
	}

	return b.String()
}

// renderStatusSection lays one kind out as aligned columns, with each row's
// problems indented beneath it. An empty kind is omitted entirely — a project
// with no databases should not have to look at a database heading.
//
// The kind names the first column rather than sitting on a line of its own: the
// first column is what identifies a row anyway, so a separate "NAME" heading
// costs a line per section to say nothing.
func renderStatusSection(title string, columns []string, rows []statusRow) string {
	if len(rows) == 0 {
		return ""
	}
	headers := append([]string{title}, columns...)
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = lipgloss.Width(h)
	}
	for _, r := range rows {
		for i, cell := range r.cells {
			if i < len(widths) && lipgloss.Width(cell) > widths[i] {
				widths[i] = lipgloss.Width(cell)
			}
		}
	}

	pad := func(cells []string) string {
		parts := make([]string, 0, len(cells))
		for i, cell := range cells {
			if i == len(cells)-1 {
				parts = append(parts, cell)
				continue
			}
			parts = append(parts, cell+strings.Repeat(" ", max(0, widths[i]-lipgloss.Width(cell))))
		}
		return strings.Join(parts, "  ")
	}

	// Every line carries the same two-column gutter, header included, so the
	// change marker never shifts a row out of alignment with its heading.
	const gutter = "  "

	var b strings.Builder
	b.WriteString("\n" + gutter + lipgloss.NewStyle().Bold(true).Foreground(colorMuted).Render(pad(headers)) + "\n")
	for _, r := range rows {
		marker := gutter
		if r.changed {
			marker = lipgloss.NewStyle().Foreground(colorInfo).Render("› ")
		}
		b.WriteString(marker + pad(r.cells) + "\n")
		for _, hint := range r.hints {
			b.WriteString("  " + lipgloss.NewStyle().Foreground(colorInfo).Render("  └ "+hint) + "\n")
		}
		for _, note := range r.notes {
			b.WriteString("  " + lipgloss.NewStyle().Foreground(colorWarning).Render("  └ "+note) + "\n")
		}
	}
	return b.String()
}

// appReadiness says what the app is doing in one cell: its control-plane status,
// and how much of what was asked for is actually up.
func appReadiness(a client.AppStatus) string {
	label := renderStatus(a.Status)
	// Mid-rollout the app is both serving and changing. Readiness alone reports
	// only the first, which is how a whole deploy used to render as a settled
	// "1/1" (#819) — so the rollout takes the cell and the counts come from it.
	if r := a.Rollout; r != nil {
		return lipgloss.NewStyle().Foreground(colorInfo).Render("↻ rolling out") +
			mutedStyle.Render(fmt.Sprintf(" %d/%d", r.Updated, r.Desired))
	}
	switch {
	case a.Mode == "serverless" && a.Ready == 0:
		return label + mutedStyle.Render(" 0 (idle)")
	case a.Desired > 0 && a.Ready < a.Desired:
		return label + lipgloss.NewStyle().Foreground(colorWarning).Render(fmt.Sprintf(" %d/%d", a.Ready, a.Desired))
	case a.Desired > 0:
		return label + mutedStyle.Render(fmt.Sprintf(" %d/%d", a.Ready, a.Desired))
	}
	return label
}

// shortImage drops the registry host and the digest, which are the same for
// every app in a project and never what someone is scanning the column for.
func shortImage(image string) string {
	if image == "" {
		return "—"
	}
	if at := strings.Index(image, "@sha256:"); at != -1 {
		image = image[:at] + "@" + image[at+8:at+8+7]
	}
	if slash := strings.LastIndex(image, "/"); slash != -1 {
		image = image[slash+1:]
	}
	return image
}

func lastRunLabel(r *client.JobRunStatus) string {
	if r == nil {
		return mutedStyle.Render("never run")
	}
	when := ""
	if r.FinishedAt != nil {
		when = " " + humanAge(*r.FinishedAt) + " ago"
	}
	switch r.Status {
	case "succeeded":
		return lipgloss.NewStyle().Foreground(colorSuccess).Render("✓ succeeded" + when)
	case "failed":
		detail := ""
		if r.ExitCode != nil {
			detail = fmt.Sprintf(" (exit %d)", *r.ExitCode)
		}
		return lipgloss.NewStyle().Foreground(colorDanger).Render("✗ failed" + when + detail)
	default:
		return lipgloss.NewStyle().Foreground(colorInfo).Render("◌ " + r.Status)
	}
}

func tlsLabel(status string) string {
	switch status {
	case "issued":
		return lipgloss.NewStyle().Foreground(colorSuccess).Render("issued")
	case "failed":
		return lipgloss.NewStyle().Foreground(colorDanger).Render("failed")
	case "":
		return mutedStyle.Render("—")
	default:
		return lipgloss.NewStyle().Foreground(colorWarning).Render(status)
	}
}

// problemNotes renders a resource's problems as the lines shown under it.
func problemNotes(problems []client.StatusProblem) []string {
	notes := make([]string, 0, len(problems))
	for _, p := range problems {
		line := p.Reason
		if p.Detail != "" {
			line += " — " + p.Detail
		}
		if p.Count > 1 {
			line += fmt.Sprintf(" (×%d)", p.Count)
		}
		notes = append(notes, line)
	}
	return notes
}

// humanAge renders a duration the way a status line wants it: coarse, short,
// and never more precise than the thing it describes.
func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func init() {
	projectStatusCmd.Flags().BoolP("watch", "w", false, "Redraw the view as the project changes")
	projectStatusCmd.Flags().Duration("interval", 2*time.Second, "How often --watch polls (minimum 1s)")
}

// runningImage is what the cluster actually reports, falling back to what the
// control plane asked for when there is no live workload to read (db-only mode,
// or an app that has never rolled out). Showing the desired image while a
// different one is serving is how a deploy looks like nothing happening.
func runningImage(a client.AppStatus) string {
	if a.RunningImage != "" {
		return a.RunningImage
	}
	return a.Image
}

// releaseLabel is the release the workload carries, falling back to the control
// plane's. Mid-rollout the two differ and the live one is the honest answer.
func releaseLabel(a client.AppStatus) string {
	release := a.RunningRelease
	if release == "" {
		release = a.Release
	}
	if release == "" {
		return mutedStyle.Render("—")
	}
	return release
}

// rolloutKey collapses a rollout into a comparable token, so --watch can tell
// "still rolling, same step" from "moved". Nil (settled) is its own value, which
// is what makes the final step out of a rollout register as a change.
func rolloutKey(r *client.RolloutStatus) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("%d/%d/%d/%d:%s", r.Updated, r.Total, r.Available, r.Desired, r.Reason)
}

// appAge is how long this version has actually been serving — the age of the
// oldest running pod, not when the deploy was requested. A rollout resets it,
// which is the point: it answers "how long has what I am talking to been up".
func appAge(a client.AppStatus) string {
	if a.Pods == nil || a.Pods.RunningSeconds == 0 {
		return mutedStyle.Render("—")
	}
	return shortDuration(time.Duration(a.Pods.RunningSeconds) * time.Second)
}

// podMovement renders what is coming up and going away right now. Empty when
// the population is steady, so a settled app carries no line at all.
func podMovement(p *client.PodPhases) string {
	if p == nil {
		return ""
	}
	parts := []string{}
	if p.Starting > 0 {
		parts = append(parts, fmt.Sprintf("%d starting (%s)", p.Starting, shortDuration(time.Duration(p.StartingSeconds)*time.Second)))
	}
	if p.Terminating > 0 {
		parts = append(parts, fmt.Sprintf("%d terminating (%s)", p.Terminating, shortDuration(time.Duration(p.TerminatingSeconds)*time.Second)))
	}
	return strings.Join(parts, " · ")
}

// podKey collapses the pod population into a comparable token for --watch, so a
// pod appearing or draining marks the row even when replica counts do not move.
func podKey(p *client.PodPhases) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("%d/%d/%d", p.Running, p.Starting, p.Terminating)
}

// shortDuration renders a span at one unit of precision — the way an age or a
// duration column wants it.
//
// A time.Duration rather than a second count: the API reports some spans as
// seconds and others as a pair of timestamps, and a helper taking seconds makes
// every caller of the second kind do the conversion at the call site.
func shortDuration(d time.Duration) string {
	seconds := int64(d.Seconds())
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh", seconds/3600)
	default:
		return fmt.Sprintf("%dd", seconds/86400)
	}
}

// configLabel says how much configuration an app carries — never what it is.
// The count is the useful part of the answer: an app with no config where you
// expected six is a diagnosis, and the values themselves are `fpcloud config
// list`'s business, behind its own permission check.
func configLabel(c *client.ConfigCount) string {
	if c == nil || c.Values == 0 {
		return mutedStyle.Render("—")
	}
	if c.Secrets == 0 {
		return fmt.Sprintf("%d", c.Values)
	}
	return fmt.Sprintf("%d (%d secret)", c.Values, c.Secrets)
}

// pollFailure renders a failed poll with what to do about it. A watch keeps
// retrying rather than exiting, so the message has to carry the remedy —
// otherwise an expired login shows an error that never changes and never says
// it is waiting for you to fix it.
func pollFailure(err error) string {
	msg := "poll failed: " + err.Error()
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
		msg += "\n\nRun `fpcloud login` in another terminal — this view picks the\nnew credentials up on its next poll, no restart needed."
	}
	return msg
}
