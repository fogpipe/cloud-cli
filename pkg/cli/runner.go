package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var runnerCmd = &cobra.Command{
	Use:     "runner",
	Aliases: []string{"runners"},
	Short:   "Manage GitHub Actions runners",
	Long: `Manage managed GitHub Actions runners.

A runner is a pool, not a machine: pods are created for one job and destroyed
when it ends, so a pool that scales to zero (--min 0, the default) costs
nothing while idle. A runner kept warm by --min is a pod that exists while
idle, and it is billed for its cpu and memory the whole time, at the rates
your org's price list shows (fpcloud billing prices). A pool serves every
repository in the GitHub account this project is connected to.

Workflows opt in by naming the pool in ` + "`runs-on`" + `.

  # connect the project to your GitHub account once
  fpcloud github connect

  # then a pool needs nothing but a name
  fpcloud runner create ci

  # then, in .github/workflows/ci.yml — the label is <project>-<name>
  #   jobs:
  #     test:
  #       runs-on: myproject-ci`,
}

// resolveRunnerID turns a runner name (or id) into an id, using the current
// project.
func resolveRunnerID(c *client.Client, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("runner name or id is required")
	}
	if looksLikeUUID(ref) {
		return ref, nil
	}
	project, err := requireProject()
	if err != nil {
		return "", fmt.Errorf("resolve runner %q: %w", ref, err)
	}
	runners, err := c.ListRunners(context.Background(), project)
	if err != nil {
		return "", err
	}
	for _, r := range runners {
		if r.Name == ref {
			return r.ID, nil
		}
	}
	return "", notFoundf("runner %q not found in project %q", ref, project)
}

// runnerCredential reads the credential flags, loading the App private key from
// its file. The key is never taken as a flag value: it is multi-line PEM, and a
// value on a command line lands in shell history and in the process table.
//
// The default source is the platform's own GitHub App, which needs no flags at
// all — `--credential` exists for the cases it cannot serve.
func runnerCredential(cmd *cobra.Command) (credential, appID, installationID, privateKey, token string, err error) {
	credential = mustString(cmd, "credential")
	appID = mustString(cmd, "github-app-id")
	installationID = mustString(cmd, "github-app-installation-id")
	token = mustString(cmd, "github-token")
	if path := mustString(cmd, "github-app-private-key-file"); path != "" {
		pem, rerr := os.ReadFile(path)
		if rerr != nil {
			return "", "", "", "", "", fmt.Errorf("read --github-app-private-key-file: %w", rerr)
		}
		privateKey = string(pem)
	}
	// Supplying a credential is unambiguous about which one is meant, so it
	// selects the source rather than being ignored for want of a flag.
	if credential == "" {
		switch {
		case token != "":
			credential = "token"
		case privateKey != "" || appID != "":
			credential = "app"
		}
	}
	return credential, appID, installationID, privateKey, token, nil
}

var runnerCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a runner pool",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		credential, appID, installationID, privateKey, token, err := runnerCredential(cmd)
		if err != nil {
			return err
		}

		req := client.CreateRunnerRequest{
			Name:                    args[0],
			DisplayName:             mustString(cmd, "display-name"),
			GitHubAccount:           mustString(cmd, "github-account"),
			RunnerGroup:             mustString(cmd, "runner-group"),
			Image:                   mustString(cmd, "image"),
			CPU:                     mustString(cmd, "cpu"),
			Memory:                  mustString(cmd, "memory"),
			Builder:                 runnerBuilderFromFlags(cmd),
			Credential:              credential,
			GitHubAppID:             appID,
			GitHubAppInstallationID: installationID,
			GitHubAppPrivateKey:     privateKey,
			GitHubToken:             token,
		}
		if cmd.Flags().Changed("min") {
			v := mustInt(cmd, "min")
			req.MinRunners = &v
		}
		if cmd.Flags().Changed("max") {
			v := mustInt(cmd, "max")
			req.MaxRunners = &v
		}

		c := getClient()
		runner, err := c.CreateRunner(context.Background(), project, req)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(runner)
		}
		fmt.Println(renderInfoBox("Runner Created", runnerInfoRows(runner)))
		fmt.Println()
		fmt.Println(mutedStyle.Render(runnerUseNote(runner)))
		fmt.Println()
		return nil
	},
}

var runnerListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List runner pools",
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		c := getClient()
		runners, err := c.ListRunners(context.Background(), project)
		if err != nil {
			return err
		}

		rows := make([][]string, len(runners))
		for i, r := range runners {
			rows[i] = []string{
				r.Name,
				runnerScope(r),
				fmt.Sprintf("%d-%d", r.MinRunners, r.MaxRunners),
				runnerActivity(r),
				runnerWaitingCell(r),
				renderStatus(r.Status),
			}
		}
		render([]string{"NAME", "SERVES", "SCALE", "RUNNERS", "WAITING", "STATUS"}, rows, runners)
		return nil
	},
}

var runnerShowCmd = &cobra.Command{
	Use:     "show <name>",
	Aliases: []string{"get", "describe"},
	Short:   "Show a runner pool",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveRunnerID(c, args[0])
		if err != nil {
			return err
		}
		runner, err := c.GetRunner(context.Background(), id)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(runner)
		}
		fmt.Println(renderInfoBox("Runner", runnerInfoRows(runner)))
		fmt.Println()
		fmt.Println(mutedStyle.Render(runnerUseNote(runner)))
		fmt.Println()
		return nil
	},
}

var runnerUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update a runner pool",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveRunnerID(c, args[0])
		if err != nil {
			return err
		}
		var req client.UpdateRunnerRequest
		setString(cmd, "display-name", &req.DisplayName)
		setString(cmd, "github-account", &req.GitHubAccount)
		setString(cmd, "runner-group", &req.RunnerGroup)
		setString(cmd, "image", &req.Image)
		setString(cmd, "cpu", &req.CPU)
		setString(cmd, "memory", &req.Memory)
		if cmd.Flags().Changed("min") {
			v := mustInt(cmd, "min")
			req.MinRunners = &v
		}
		if cmd.Flags().Changed("max") {
			v := mustInt(cmd, "max")
			req.MaxRunners = &v
		}
		credential, appID, installationID, privateKey, token, err := runnerCredential(cmd)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("credential") {
			req.Credential = &credential
		}
		if cmd.Flags().Changed("github-app-id") {
			req.GitHubAppID = &appID
		}
		if cmd.Flags().Changed("github-app-installation-id") {
			req.GitHubAppInstallationID = &installationID
		}
		if cmd.Flags().Changed("github-app-private-key-file") {
			req.GitHubAppPrivateKey = &privateKey
		}
		if cmd.Flags().Changed("github-token") {
			req.GitHubToken = &token
		}

		// The builder rides along in the same patch as the runner's own size:
		// the project's caps weigh the two together, so a pool over its cap can
		// only come back under if one request carries both.
		if err := setRunnerBuilder(c, id, cmd, &req); err != nil {
			return err
		}
		runner, err := c.UpdateRunner(context.Background(), id, req)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(runner)
		}
		fmt.Println(renderInfoBox("Runner Updated", runnerInfoRows(runner)))
		fmt.Println()
		return nil
	},
}

var runnerDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Aliases: []string{"rm"},
	Short:   "Delete a runner pool",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		id, err := resolveRunnerID(c, args[0])
		if err != nil {
			return err
		}
		if err := c.DeleteRunner(context.Background(), id); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Runner %q deleted.", args[0]),
		))
		return nil
	},
}

var runnerRestartCmd = &cobra.Command{
	Use:   "restart <name>",
	Short: "Recycle a runner pool, letting running jobs finish first",
	Long: `Replace every runner in the pool and its listener, so a pool that has
stopped taking work is recovered without deleting anything by hand.

Draining by default: a runner serving a job finishes it before it goes, and
this command says which jobs it is waiting on while it waits. An ephemeral
runner takes exactly one job, so the drain is bounded by the longest running
job, never open-ended. --force recycles at once and kills whatever is
running — for a pool wedged on a runner that will never finish. The safe
action is the default and the destructive one has to be typed
(fogpipe/cloud-workspace#145).

The restart is accepted and carried out by the platform (ADR-079); --no-wait
returns as soon as it is accepted, and ` + "`fpcloud runner show`" + ` reports
the restart in progress.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		yes, _ := cmd.Flags().GetBool("yes")
		noWait, _ := cmd.Flags().GetBool("no-wait")
		c := getClient()
		id, err := resolveRunnerID(c, args[0])
		if err != nil {
			return err
		}
		if force && !yes {
			ok, err := confirm(
				fmt.Sprintf("Force-restart runner pool %q?", args[0]),
				"Every runner is replaced now, and any job one of them is running is killed — GitHub does not re-offer a job whose runner died.",
				"Yes, kill running jobs")
			if err != nil || !ok {
				return err
			}
		}
		runner, err := c.RestartRunner(context.Background(), id, client.RestartRunnerRequest{Force: force})
		if err != nil {
			return err
		}
		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			if noWait {
				return renderData(runner)
			}
		} else if noWait {
			fmt.Println(renderInfoBox("Restart Accepted", [][]string{
				{"Runner", runner.Name},
				{"Mode", restartMode(force)},
				{"", mutedStyle.Render("Carried out by the platform; `fpcloud runner show " + args[0] + "` reports it.")},
			}))
			return nil
		}
		var last string
		progress := func(r *client.Runner) {
			if isStructured(outputFormat) {
				return
			}
			note := runnerRestartNote(r)
			if note != last {
				fmt.Fprintln(os.Stderr, mutedStyle.Render("  "+note))
				last = note
			}
		}
		done, err := c.WaitRunnerRestarted(context.Background(), id, 3*time.Second, progress)
		if err != nil {
			return err
		}
		if isStructured(outputFormat) {
			return renderData(done)
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Runner %q restarted: every runner and the listener are new.", args[0]),
		))
		return nil
	},
}

func restartMode(force bool) string {
	if force {
		return "immediate — running jobs killed"
	}
	return "drain — running jobs finish first"
}

// runnerRestartNote says what a restart in progress is waiting for, because a
// drain that silently waits on a twelve-minute job is indistinguishable from a
// hang (docs/reading-a-zero.md, fogpipe/cloud-workspace#145).
func runnerRestartNote(r *client.Runner) string {
	if r.Restart == nil {
		return "restart complete"
	}
	if r.Restart.Note != "" && len(r.Restart.Waiting) == 0 {
		return r.Restart.Note
	}
	if len(r.Restart.Waiting) == 0 {
		return "waiting for the platform to replace the pool's runners and listener"
	}
	jobs := make([]string, 0, len(r.Restart.Waiting))
	for _, in := range r.Restart.Waiting {
		jobs = append(jobs, fmt.Sprintf("%q on %s (%s)", in.Job, in.Repository, time.Since(in.StartedAt).Round(time.Second)))
	}
	return fmt.Sprintf("draining: waiting for %d running job(s) to finish — %s", len(jobs), strings.Join(jobs, ", "))
}

// runnerScope renders the GitHub account the pool serves — every repository in
// it. Derived from the project's connection, never typed.
// runnerBusy is "N of M" — runners executing a job beside the most the pool
// may run at once — because a bare count under any heading is ambiguous in a
// way the pair is not (fogpipe/cloud-workspace#146). M is what the org's
// ceiling admits when that is below the pool's own max.
func runnerBusy(r *client.Runner) string {
	most := r.MaxRunners
	if r.AdmittedRunners > 0 && r.AdmittedRunners < most {
		most = r.AdmittedRunners
	}
	return fmt.Sprintf("%d of %d", r.RunningRunners, most)
}

// runnerWaiting is the queue as GitHub sees it: jobs assigned to the pool that
// no runner has started. It is the whole diagnosis — an idle pool and one
// that cannot schedule look alike until this number is known — so a queue
// that could not be read is said to be unreadable, never rendered as empty
// (docs/reading-a-zero.md, fogpipe/cloud-workspace#146).
func runnerWaiting(r *client.Runner) string {
	if r.Queue != nil {
		if r.Queue.Waiting == 0 {
			return fmt.Sprintf("none (as of %s)", r.Queue.AsOf.Local().Format("15:04:05"))
		}
		return fmt.Sprintf("%d job(s) assigned by GitHub and not started (as of %s)", r.Queue.Waiting, r.Queue.AsOf.Local().Format("15:04:05"))
	}
	for _, p := range r.Problems {
		if p.Reason == "QueueUnread" {
			return mutedStyle.Render("unreadable — " + p.Detail)
		}
	}
	return mutedStyle.Render("not reported by this control plane")
}

// runnerWaitingCell is runnerWaiting for a table column.
func runnerWaitingCell(r *client.Runner) string {
	if r.Queue != nil {
		return fmt.Sprintf("%d", r.Queue.Waiting)
	}
	return mutedStyle.Render("?")
}

// runnerUseNote says where the pool's label works, not only what it is. GitHub
// registers self-hosted runners per account, so a workflow in a repository
// outside the one the pool serves names a label nobody offers it and queues
// forever with no error on either side (fogpipe/cloud-workspace#305) — the
// label alone reads as if any repository could use it.
func runnerUseNote(r *client.Runner) string {
	return fmt.Sprintf("  Use it from a workflow in a github.com/%s repository with: runs-on: %s\n"+
		"  A workflow in any other account's repository never sees this pool: its job queues forever.",
		runnerScope(r), strings.Join(r.Labels, ", "))
}

// repoOutsideAccount reports whether an owner/name repository lives outside
// the GitHub account a project's pools serve. GitHub logins are
// case-insensitive.
func repoOutsideAccount(repo, account string) bool {
	owner, _, ok := strings.Cut(repo, "/")
	return ok && account != "" && owner != "" && !strings.EqualFold(owner, account)
}

func runnerScope(r *client.Runner) string {
	return strings.TrimPrefix(strings.TrimPrefix(r.GitHubConfigURL, "https://github.com/"), "https://")
}

// runnerActivity says what the pool's runners are doing, not how many objects
// exist: a runner executing a job and one waiting for a pod the ceiling refuses
// were one "active" count, and the two call for opposite responses
// (fogpipe/cloud-workspace#120). A control plane that sends only the sum is
// rendered as the sum.
func runnerActivity(r *client.Runner) string {
	if r.RunningRunners == 0 && r.PendingRunners == 0 {
		return fmt.Sprintf("%d active", r.CurrentRunners)
	}
	return fmt.Sprintf("%d running, %d pending", r.RunningRunners, r.PendingRunners)
}

func runnerInfoRows(r *client.Runner) [][]string {
	scale := fmt.Sprintf("%d-%d (%s)", r.MinRunners, r.MaxRunners, runnerActivity(r))
	if r.AdmittedRunners > 0 {
		scale = fmt.Sprintf("%d-%d (%s, ceiling admits %d)", r.MinRunners, r.MaxRunners, runnerActivity(r), r.AdmittedRunners)
	}
	rows := [][]string{
		{"Name", r.Name},
		{"Serves", runnerScope(r)},
		{"runs-on", strings.Join(r.Labels, ", ")},
		{"Group", r.RunnerGroup},
		{"Scale", scale},
		{"Busy", runnerBusy(r)},
		{"Waiting", runnerWaiting(r)},
		{"Status", renderStatus(r.Status)},
	}
	if r.Restart != nil {
		rows = append(rows, []string{"Restart", restartMode(r.Restart.Force) + ", asked " +
			time.Since(r.Restart.RequestedAt).Round(time.Second).String() + " ago — " + runnerRestartNote(r)})
	}
	if r.Builder != nil {
		rows = append(rows, []string{"Builder", fmt.Sprintf("rootless BuildKit, %s (BUILDKIT_HOST is set in the job)",
			strings.TrimSpace(r.Builder.CPU+" "+r.Builder.Memory))})
	}
	switch r.Credential {
	case "platform":
		rows = append(rows, []string{"Credential", fmt.Sprintf("Fogpipe GitHub App (installation %s)", r.GitHubAppInstallationID)})
	case "app":
		rows = append(rows, []string{"Credential", fmt.Sprintf("your GitHub App %s (installation %s)", r.GitHubAppID, r.GitHubAppInstallationID)})
	default:
		rows = append(rows, []string{"Credential", "token"})
	}
	if r.Image != "" {
		rows = append(rows, []string{"Image", r.Image})
	}
	if r.CPU != "" || r.Memory != "" {
		rows = append(rows, []string{"Runner limits", strings.TrimSpace(r.CPU + " " + r.Memory)})
	}
	if r.Message != "" {
		rows = append(rows, []string{"Note", r.Message})
	}
	// Listed after the note rather than folded into it: a pool can have several,
	// and the count is the difference between one unlucky job and a pool that is
	// too small for the work being put through it.
	for _, p := range r.Problems {
		detail := p.Detail
		if p.Count > 1 {
			detail = fmt.Sprintf("%s (×%d)", detail, p.Count)
		}
		rows = append(rows, []string{"Problem", strings.TrimSpace(p.Reason + " — " + detail)})
	}
	// One row per live runner: what each is doing, from the platform itself —
	// never reconstructed from GitHub's API (fogpipe/cloud-workspace#129).
	for _, in := range r.Instances {
		rows = append(rows, []string{"Runner", runnerInstanceLine(in)})
	}
	return rows
}

// runnerInstanceLine renders one live runner: its state, how long it has run,
// and — for a busy one — the job it is serving and the run it belongs to.
func runnerInstanceLine(in client.RunnerInstance) string {
	age := time.Since(in.StartedAt).Round(time.Second)
	switch in.State {
	case "busy":
		return fmt.Sprintf("%s — %q on %s (run %d), %s", in.Name, in.Job, in.Repository, in.WorkflowRunID, age)
	default:
		return fmt.Sprintf("%s — %s, %s", in.Name, in.State, age)
	}
}

// runnerBuilderFromFlags reads the builder a create asked for, or nil for a pool
// that builds nothing. Naming a size implies the builder, so `--builder-memory
// 8Gi` alone does what it looks like; an unset size takes the platform's
// default for a builder, which is not the runner's own size (ADR-071).
func runnerBuilderFromFlags(cmd *cobra.Command) *client.RunnerBuilder {
	cpu, memory := mustString(cmd, "builder-cpu"), mustString(cmd, "builder-memory")
	if !mustBool(cmd, "builder") && cpu == "" && memory == "" {
		return nil
	}
	return &client.RunnerBuilder{CPU: cpu, Memory: memory}
}

// setRunnerBuilder fills in what an update said about the builder, and leaves
// the patch alone when it said nothing. Silence is not "remove it": a pool
// updated for its display name keeps the builder it has.
//
// The field replaces the builder rather than patching it, so a size the command
// did not name is read back off the pool rather than dropped — `--builder-memory
// 8Gi` changes the memory and leaves the CPU where the tenant put it. That read
// is the only reason this needs the pool at all, so it happens only when a size
// was named and left half-stated.
func setRunnerBuilder(c *client.Client, id string, cmd *cobra.Command, req *client.UpdateRunnerRequest) error {
	if mustBool(cmd, "no-builder") {
		req.NoBuilder = true
		return nil
	}
	builder := runnerBuilderFromFlags(cmd)
	if builder == nil {
		return nil
	}
	if builder.CPU == "" || builder.Memory == "" {
		current, err := c.GetRunner(context.Background(), id)
		if err != nil {
			return err
		}
		if current.Builder != nil {
			if builder.CPU == "" {
				builder.CPU = current.Builder.CPU
			}
			if builder.Memory == "" {
				builder.Memory = current.Builder.Memory
			}
		}
	}
	req.Builder = builder
	return nil
}

// runnerSpecFlags are the knobs shared by create and update.
func runnerSpecFlags(cmd *cobra.Command) {
	cmd.Flags().String("github-account", "", "GitHub account the runner serves (only with --credential app or token; the Fogpipe App uses this project's connection)")
	cmd.Flags().String("runner-group", "", "GitHub runner group to join (default Default)")
	cmd.Flags().Int("min", 0, "Runners kept idle and ready (default 0, scale to zero); each one is billed for its cpu and memory while idle")
	cmd.Flags().Int("max", 2, "Jobs the pool runs at once")
	cmd.Flags().String("image", "", "Runner image (defaults to the platform's)")
	cmd.Flags().String("cpu", "", "CPU limit for the runner itself, e.g. 2")
	cmd.Flags().String("memory", "", "Memory limit for the runner itself, e.g. 4Gi")
	cmd.Flags().Bool("builder", false, "Run a rootless image builder alongside each job (sets BUILDKIT_HOST)")
	cmd.Flags().String("builder-cpu", "", "CPU limit for the builder, e.g. 1 (implies --builder)")
	cmd.Flags().String("builder-memory", "", "Memory limit for the builder, e.g. 2Gi (implies --builder)")
	cmd.Flags().String("display-name", "", "Human-readable label")
	cmd.Flags().String("credential", "", "How the runner authenticates: platform (default, the Fogpipe GitHub App), app (your own), token")
	cmd.Flags().String("github-app-id", "", "GitHub App id (--credential app)")
	cmd.Flags().String("github-app-installation-id", "", "GitHub App installation id (--credential app)")
	cmd.Flags().String("github-app-private-key-file", "", "Path to your GitHub App private key, .pem (--credential app)")
	cmd.Flags().String("github-token", "", "Personal access token (--credential token)")
}

func init() {
	runnerSpecFlags(runnerCreateCmd)
	runnerSpecFlags(runnerUpdateCmd)
	runnerUpdateCmd.Flags().Bool("no-builder", false, "Remove the pool's image builder")
	runnerUpdateCmd.MarkFlagsMutuallyExclusive("no-builder", "builder")
	runnerUpdateCmd.MarkFlagsMutuallyExclusive("no-builder", "builder-cpu")
	runnerUpdateCmd.MarkFlagsMutuallyExclusive("no-builder", "builder-memory")

	runnerRestartCmd.Flags().Bool("force", false, "Recycle at once, killing any job a runner is running (default: let running jobs finish)")
	runnerRestartCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt on --force")
	runnerRestartCmd.Flags().Bool("no-wait", false, "Return once the restart is accepted instead of waiting for it to finish")
	runnerCmd.AddCommand(runnerCreateCmd, runnerListCmd, runnerShowCmd, runnerUpdateCmd, runnerDeleteCmd, runnerRestartCmd)
	rootCmd.AddCommand(runnerCmd)
}
