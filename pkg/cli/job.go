package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var jobCmd = &cobra.Command{
	Use:     "job",
	Aliases: []string{"jobs"},
	Short:   "Manage scheduled jobs (cron)",
	Long: `Manage scheduled jobs.

A job is one schedule plus what to run: your own container image, or an HTTP
request to an endpoint (no image needed). Point a job at an app with --app and
it runs with that app's image, config and secrets — so a token never has to be
copied into a second place.

  # POST your own endpoint every 15 minutes
  fpcloud job create sweep --app api --schedule "*/15 * * * *" --http-target /internal/sweep

  # run the app's image with a different entrypoint, nightly in local time
  fpcloud job create nightly --app api --schedule "0 3 * * *" \
      --timezone Europe/Stockholm --command "npm run cleanup"`,
}

// resolveJobID turns a job name (or id) into an id, using the current project.
func resolveJobID(c *client.Client, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("job name or id is required")
	}
	if looksLikeUUID(ref) {
		return ref, nil
	}
	project, err := requireProject()
	if err != nil {
		return "", fmt.Errorf("resolve job %q: %w", ref, err)
	}
	jobs, err := c.ListJobs(context.Background(), project)
	if err != nil {
		return "", err
	}
	for _, j := range jobs {
		if j.Name == ref {
			return j.ID, nil
		}
	}
	return "", fmt.Errorf("job %q not found in project %q", ref, project)
}

// parseHeaderFlags turns repeated --header "Name: value" (or "Name=value")
// flags into the header map. A $VAR in the value is expanded from the job's env
// when it fires, not here.
func parseHeaderFlags(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(values))
	for _, raw := range values {
		name, value, ok := strings.Cut(raw, ":")
		if !ok {
			name, value, ok = strings.Cut(raw, "=")
		}
		if !ok {
			return nil, fmt.Errorf("invalid --header %q (expected \"Name: value\")", raw)
		}
		headers[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return headers, nil
}

// parseRetention reads a run-retention window: a Go duration extended with day
// and week units (36h, 7d, 2w), or "never" for no age bound at all. Days and
// weeks are the units retention is actually written in, and time.ParseDuration
// stops at hours — 604800 is not a number anyone should have to type or read.
func parseRetention(raw string) (int, error) {
	invalid := fmt.Errorf("invalid retention %q (use e.g. 24h, 7d, 2w, or \"never\")", raw)
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || v == "0" || v == "never" || v == "forever" {
		return 0, nil
	}
	for suffix, unit := range map[string]int{"d": 24 * 60 * 60, "w": 7 * 24 * 60 * 60} {
		n, ok := strings.CutSuffix(v, suffix)
		if !ok {
			continue
		}
		count, err := strconv.Atoi(n)
		if err != nil || count < 0 {
			return 0, invalid
		}
		return count * unit, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, invalid
	}
	return int(d.Seconds()), nil
}

// formatRetention renders a window the way it was most likely written.
func formatRetention(secs int) string {
	if secs <= 0 {
		return "forever"
	}
	d := time.Duration(secs) * time.Second
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	return d.String()
}

// setRetention applies a retention flag to a patch field, leaving it nil (and so
// the stored value untouched) when the flag wasn't given.
func setRetention(cmd *cobra.Command, name string, target **int) error {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	secs, err := parseRetention(mustString(cmd, name))
	if err != nil {
		return fmt.Errorf("--%s: %w", name, err)
	}
	*target = &secs
	return nil
}

var jobCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a scheduled job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		headers, err := parseHeaderFlags(mustStringSlice(cmd, "header"))
		if err != nil {
			return err
		}

		req := client.CreateJobRequest{
			Name:        args[0],
			DisplayName: mustString(cmd, "display-name"),
			App:         mustString(cmd, "app"),
			Schedule:    mustString(cmd, "schedule"),
			Timezone:    mustString(cmd, "timezone"),
			Concurrency: mustString(cmd, "concurrency"),
			Suspended:   mustBool(cmd, "suspended"),
			Image:       mustString(cmd, "image"),
			Args:        mustStringSlice(cmd, "arg"),
			HTTPURL:     mustString(cmd, "http-target"),
			HTTPMethod:  mustString(cmd, "method"),
			HTTPHeaders: headers,
			HTTPBody:    mustString(cmd, "body"),
		}
		if v := mustString(cmd, "command"); v != "" {
			req.Command = []string{v}
		}
		if cmd.Flags().Changed("max-retries") {
			v := mustInt(cmd, "max-retries")
			req.MaxRetries = &v
		}
		if cmd.Flags().Changed("timeout") {
			v := mustInt(cmd, "timeout")
			req.Timeout = &v
		}
		if cmd.Flags().Changed("keep-runs") {
			v := mustInt(cmd, "keep-runs")
			req.KeepRuns = &v
		}
		if err := setRetention(cmd, "retain-succeeded", &req.RetainSucceeded); err != nil {
			return err
		}
		if err := setRetention(cmd, "retain-failed", &req.RetainFailed); err != nil {
			return err
		}

		c := getClient()
		job, err := c.CreateJob(context.Background(), project, req)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(job)
		}
		fmt.Println(renderInfoBox("Scheduled Job Created", jobInfoRows(job)))
		fmt.Println()
		fmt.Println(mutedStyle.Render(fmt.Sprintf("  Run it now without waiting: fpcloud job run %s", job.Name)))
		fmt.Println()
		return nil
	},
}

var jobListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List scheduled jobs",
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		c := getClient()
		jobs, err := c.ListJobs(context.Background(), project)
		if err != nil {
			return err
		}

		rows := make([][]string, len(jobs))
		for i, j := range jobs {
			rows[i] = []string{j.Name, j.Schedule, j.Timezone, jobTargetSummary(j), scheduleState(j), lastRunSummary(j.LastRun)}
		}
		render([]string{"NAME", "SCHEDULE", "TZ", "TARGET", "STATE", "LAST RUN"}, rows, jobs)
		return nil
	},
}

var jobShowCmd = &cobra.Command{
	Use:     "show <name>",
	Aliases: []string{"get", "describe"},
	Short:   "Show a scheduled job",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveJobID(c, args[0])
		if err != nil {
			return err
		}
		job, err := c.GetJob(context.Background(), id)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(job)
		}
		fmt.Println(renderInfoBox("Scheduled Job", jobInfoRows(job)))
		fmt.Println()
		return nil
	},
}

var jobUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update a scheduled job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveJobID(c, args[0])
		if err != nil {
			return err
		}

		var req client.UpdateJobRequest
		setString(cmd, "schedule", &req.Schedule)
		setString(cmd, "timezone", &req.Timezone)
		setString(cmd, "concurrency", &req.Concurrency)
		setString(cmd, "display-name", &req.DisplayName)
		setString(cmd, "image", &req.Image)
		setString(cmd, "http-target", &req.HTTPURL)
		setString(cmd, "method", &req.HTTPMethod)
		setString(cmd, "body", &req.HTTPBody)
		if cmd.Flags().Changed("command") {
			v := []string{mustString(cmd, "command")}
			req.Command = &v
		}
		if cmd.Flags().Changed("arg") {
			v := mustStringSlice(cmd, "arg")
			req.Args = &v
		}
		if cmd.Flags().Changed("header") {
			headers, herr := parseHeaderFlags(mustStringSlice(cmd, "header"))
			if herr != nil {
				return herr
			}
			req.HTTPHeaders = &headers
		}
		if cmd.Flags().Changed("max-retries") {
			v := mustInt(cmd, "max-retries")
			req.MaxRetries = &v
		}
		if cmd.Flags().Changed("timeout") {
			v := mustInt(cmd, "timeout")
			req.Timeout = &v
		}
		if cmd.Flags().Changed("keep-runs") {
			v := mustInt(cmd, "keep-runs")
			req.KeepRuns = &v
		}
		if err := setRetention(cmd, "retain-succeeded", &req.RetainSucceeded); err != nil {
			return err
		}
		if err := setRetention(cmd, "retain-failed", &req.RetainFailed); err != nil {
			return err
		}
		if cmd.Flags().Changed("suspended") {
			v := mustBool(cmd, "suspended")
			req.Suspended = &v
		}

		job, err := c.UpdateJob(context.Background(), id, req)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(job)
		}
		fmt.Println(renderInfoBox("Scheduled Job Updated", jobInfoRows(job)))
		fmt.Println()
		return nil
	},
}

var jobDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Aliases: []string{"rm"},
	Short:   "Delete a scheduled job",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveJobID(c, args[0])
		if err != nil {
			return err
		}
		if err := c.DeleteJob(context.Background(), id); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Job %q deleted.", args[0]),
		))
		return nil
	},
}

var jobRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Run a scheduled job now",
	Long:  "Fire a job immediately, outside its schedule — so verifying it doesn't mean waiting for the next window.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveJobID(c, args[0])
		if err != nil {
			return err
		}
		run, err := c.RunJob(context.Background(), id)
		if err != nil {
			return err
		}

		if !mustBool(cmd, "wait") {
			if isStructured(rootCmd.Flag("output").Value.String()) {
				return renderData(run)
			}
			fmt.Println(successBox.Render(
				lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
					fmt.Sprintf(" Run %s started.", run.RunName),
			))
			fmt.Println()
			fmt.Println(mutedStyle.Render(fmt.Sprintf("  Follow it: fpcloud job logs %s --run %s", args[0], run.RunName)))
			fmt.Println()
			return nil
		}

		finished, err := waitForRun(c, id, run.RunName, time.Duration(mustInt(cmd, "wait-timeout"))*time.Second)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(finished)
		}
		logs, _ := c.GetJobLogs(context.Background(), id, run.RunName)
		if strings.TrimSpace(logs) != "" {
			fmt.Println(strings.TrimRight(logs, "\n"))
			fmt.Println()
		}
		fmt.Println(renderInfoBox("Job Run", runInfoRows(finished)))
		fmt.Println()
		if finished.Status == "failed" {
			return fmt.Errorf("run %s failed", finished.RunName)
		}
		return nil
	},
}

var jobRunsCmd = &cobra.Command{
	Use:     "runs <name>",
	Aliases: []string{"history"},
	Short:   "List a job's runs",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveJobID(c, args[0])
		if err != nil {
			return err
		}
		runs, err := c.ListJobRuns(context.Background(), id)
		if err != nil {
			return err
		}

		rows := make([][]string, len(runs))
		for i, r := range runs {
			rows[i] = []string{r.RunName, r.Trigger, renderStatus(r.Status), exitCodeText(r.ExitCode), runStartedText(r), runDurationText(r)}
		}
		render([]string{"RUN", "TRIGGER", "STATUS", "EXIT", "STARTED", "DURATION"}, rows, runs)
		return nil
	},
}

var jobLogsCmd = &cobra.Command{
	Use:   "logs <name>",
	Short: "Show the output of a job run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveJobID(c, args[0])
		if err != nil {
			return err
		}
		logs, err := c.GetJobLogs(context.Background(), id, mustString(cmd, "run"))
		if err != nil {
			return err
		}
		if strings.TrimSpace(logs) == "" {
			fmt.Println(mutedStyle.Render("  No output captured for this run."))
			return nil
		}
		fmt.Println(strings.TrimRight(logs, "\n"))
		return nil
	},
}

// waitForRun polls a run until it leaves the running state or the timeout hits.
func waitForRun(c *client.Client, jobID, runName string, timeout time.Duration) (*client.JobRun, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		runs, err := c.ListJobRuns(context.Background(), jobID)
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			if r.RunName != runName {
				continue
			}
			if r.Status != "running" {
				return r, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("run %s did not finish within %s", runName, timeout)
		}
		time.Sleep(2 * time.Second)
	}
}

// jobTargetSummary is the one-column description of what a job runs.
func jobTargetSummary(j *client.Job) string {
	if j.Target == client.JobTargetHTTP {
		return fmt.Sprintf("%s %s", j.HTTPMethod, j.HTTPURL)
	}
	if j.AppName != "" && j.Image == "" {
		return "image of " + j.AppName
	}
	return j.Image
}

// scheduleState says whether the schedule is live, in the words the user set it
// with — "suspended" is a state, not a failure.
func scheduleState(j *client.Job) string {
	if j.Suspended {
		return mutedStyle.Render("suspended")
	}
	return renderStatus("active")
}

func lastRunSummary(r *client.JobRun) string {
	if r == nil {
		return mutedStyle.Render("never")
	}
	when := ""
	if r.StartedAt != nil {
		when = " " + humanizeSince(*r.StartedAt)
	}
	return renderStatus(r.Status) + when
}

func jobInfoRows(j *client.Job) [][]string {
	rows := [][]string{
		{"Name", j.Name},
		{"Schedule", fmt.Sprintf("%s (%s)", j.Schedule, j.Timezone)},
		{"Target", jobTargetSummary(j)},
	}
	if j.AppName != "" {
		rows = append(rows, []string{"App", j.AppName})
	}
	if len(j.Command) > 0 {
		rows = append(rows, []string{"Command", strings.Join(j.Command, " ")})
	}
	rows = append(rows,
		[]string{"Concurrency", j.Concurrency},
		[]string{"Retries", fmt.Sprintf("%d", j.MaxRetries)},
		[]string{"Timeout", fmt.Sprintf("%ds", j.Timeout)},
		[]string{"Retention", fmt.Sprintf("%d runs · completed %s · errored %s",
			j.KeepRuns, formatRetention(j.RetainSucceeded), formatRetention(j.RetainFailed))},
		[]string{"State", scheduleState(j)},
	)
	if j.LastRun != nil {
		rows = append(rows, []string{"Last run", lastRunSummary(j.LastRun)})
	}
	return rows
}

func runInfoRows(r *client.JobRun) [][]string {
	rows := [][]string{
		{"Run", r.RunName},
		{"Trigger", r.Trigger},
		{"Status", renderStatus(r.Status)},
	}
	if r.ExitCode != nil {
		rows = append(rows, []string{"Exit code", fmt.Sprintf("%d", *r.ExitCode)})
	}
	if r.DurationMs != nil {
		rows = append(rows, []string{"Duration", (time.Duration(*r.DurationMs) * time.Millisecond).String()})
	}
	return rows
}

func exitCodeText(code *int) string {
	if code == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *code)
}

func runStartedText(r *client.JobRun) string {
	if r.StartedAt == nil {
		return "-"
	}
	return humanizeSince(*r.StartedAt)
}

func runDurationText(r *client.JobRun) string {
	if r.DurationMs == nil {
		return "-"
	}
	return (time.Duration(*r.DurationMs) * time.Millisecond).Round(time.Second).String()
}

// humanizeSince renders a timestamp as an age ("4m ago"), which is what someone
// scanning a run list actually wants to know.
func humanizeSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// --- flag helpers ---

func mustString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func mustStringSlice(cmd *cobra.Command, name string) []string {
	v, _ := cmd.Flags().GetStringArray(name)
	return v
}

func mustInt(cmd *cobra.Command, name string) int {
	v, _ := cmd.Flags().GetInt(name)
	return v
}

func mustBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

// setString points target at the flag's value only when the user set it, so an
// untouched field is left alone by the patch.
func setString(cmd *cobra.Command, name string, target **string) {
	if !cmd.Flags().Changed(name) {
		return
	}
	v := mustString(cmd, name)
	*target = &v
}

// jobSpecFlags are the knobs shared by create and update.
func jobSpecFlags(cmd *cobra.Command) {
	cmd.Flags().String("schedule", "", "Cron schedule, e.g. \"*/15 * * * *\"")
	cmd.Flags().String("timezone", "", "Timezone the schedule is read in (default UTC), e.g. Europe/Stockholm")
	cmd.Flags().String("concurrency", "", "What happens when a run overruns the next fire: forbid (default), replace, allow")
	cmd.Flags().Int("max-retries", 3, "Retries after a failed run")
	cmd.Flags().Int("timeout", 300, "Seconds a single run may take before it is killed")
	cmd.Flags().Int("keep-runs", 10, "How many runs to keep in the history")
	cmd.Flags().String("retain-succeeded", "", "How long a completed run is kept, e.g. 24h, 7d (default), or \"never\" for no age limit")
	cmd.Flags().String("retain-failed", "", "How long an errored run is kept, e.g. 30d (default), or \"never\" for no age limit")
	cmd.Flags().Bool("suspended", false, "Pause the schedule (manual runs still work)")
	cmd.Flags().String("display-name", "", "Human-readable label")
	cmd.Flags().String("image", "", "Container image to run (defaults to the --app image)")
	cmd.Flags().String("command", "", "Entrypoint override for a container job")
	cmd.Flags().StringArray("arg", nil, "Argument for a container job (repeatable)")
	cmd.Flags().String("http-target", "", "URL or app path to request instead of running an image, e.g. /internal/sweep")
	cmd.Flags().String("method", "", "HTTP method for --http-target (default POST)")
	cmd.Flags().StringArray("header", nil, "Header for --http-target, \"Name: value\" ($VAR resolves from the app's config; repeatable)")
	cmd.Flags().String("body", "", "Request body for --http-target")
}

func init() {
	jobSpecFlags(jobCreateCmd)
	jobCreateCmd.Flags().String("app", "", "App whose image, config and identity the job inherits")
	_ = jobCreateCmd.MarkFlagRequired("schedule")

	jobSpecFlags(jobUpdateCmd)

	jobRunCmd.Flags().Bool("wait", false, "Wait for the run to finish and print its output")
	jobRunCmd.Flags().Int("wait-timeout", 300, "Seconds to wait with --wait")
	jobLogsCmd.Flags().String("run", "", "Run name (default: the most recent run)")

	jobCmd.AddCommand(jobCreateCmd, jobListCmd, jobShowCmd, jobUpdateCmd, jobDeleteCmd, jobRunCmd, jobRunsCmd, jobLogsCmd)
	rootCmd.AddCommand(jobCmd)
}
