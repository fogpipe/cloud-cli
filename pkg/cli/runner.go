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
	Use:   "runner",
	Short: "Manage this project's GitHub Actions runner",
	Long: `Manage this project's managed GitHub Actions runner.

A project has one runner. It is not a machine: a pod is created for one job
and destroyed when the job ends, so it costs nothing while idle and is billed
per minute a job runs, at the rate for its size (fpcloud billing prices). It
serves every repository in the GitHub account this project is connected to. With
your own credential it can instead serve a single repository.

Two things to choose: --size, what one job gets (small, medium or large), and
--max, how many jobs run at once.

Workflows opt in by naming the project's label in ` + "`runs-on`" + `.

  # connect the project to your GitHub account once
  fpcloud github connect

  # then the runner needs nothing
  fpcloud runner create

  # then, in .github/workflows/ci.yml — the label is <project>-ci
  #   jobs:
  #     test:
  #       runs-on: myproject-ci`,
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
	Use:   "create",
	Short: "Create this project's runner",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		credential, appID, installationID, privateKey, token, err := runnerCredential(cmd)
		if err != nil {
			return err
		}

		services, err := runnerServicesFromFlags(cmd)
		if err != nil {
			return err
		}

		req := client.CreateRunnerRequest{
			GitHubScope:             mustString(cmd, "github-scope"),
			RunnerGroup:             mustString(cmd, "runner-group"),
			Size:                    mustString(cmd, "size"),
			Builder:                 runnerBuilderFromFlags(cmd),
			Services:                services,
			Credential:              credential,
			GitHubAppID:             appID,
			GitHubAppInstallationID: installationID,
			GitHubAppPrivateKey:     privateKey,
			GitHubToken:             token,
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

var runnerShowCmd = &cobra.Command{
	Use:     "show",
	Aliases: []string{"get", "describe"},
	Short:   "Show this project's runner",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		c := getClient()
		runner, err := c.GetRunner(context.Background(), project)
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
	Use:   "update",
	Short: "Update this project's runner",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		c := getClient()
		var req client.UpdateRunnerRequest
		setString(cmd, "github-scope", &req.GitHubScope)
		setString(cmd, "runner-group", &req.RunnerGroup)
		setString(cmd, "size", &req.Size)
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
		// the org's ceiling weighs the two together, so a runner over its cap
		// can only come back under if one request carries both.
		if err := setRunnerBuilder(c, project, cmd, &req); err != nil {
			return err
		}
		// Replace-in-full, so naming any --service restates the whole set — the
		// same shape as the API's field, and the only one a caps check over the
		// whole pod can act on. --no-services is how a patch says "none", since
		// no value of the flag can.
		if mustBool(cmd, "no-services") {
			empty := []client.RunnerService{}
			req.Services = &empty
		} else if services, err := runnerServicesFromFlags(cmd); err != nil {
			return err
		} else if services != nil {
			req.Services = &services
		}
		runner, err := c.UpdateRunner(context.Background(), project, req)
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
	Use:     "delete",
	Aliases: []string{"rm"},
	Short:   "Delete this project's runner",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		project, err := requireProject()
		if err != nil {
			return err
		}
		c := getClient()
		if err := c.DeleteRunner(context.Background(), project); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				" Runner deleted.",
		))
		return nil
	},
}

var runnerRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Recycle this project's runner, letting running jobs finish first",
	Long: `Replace every runner pod and the listener, so a runner that has stopped
taking work is recovered without deleting anything by hand.

Draining by default: a pod serving a job finishes it before it goes, and this
command says which jobs it is waiting on while it waits. An ephemeral runner
takes exactly one job, so the drain is bounded by the longest running job,
never open-ended. --force recycles at once and kills whatever is running — for
a runner wedged on a pod that will never finish. The safe action is the
default and the destructive one has to be typed (fogpipe/cloud-workspace#145).

The restart is accepted and carried out by the platform (ADR-079); --no-wait
returns as soon as it is accepted, and ` + "`fpcloud runner show`" + ` reports
the restart in progress.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		yes, _ := cmd.Flags().GetBool("yes")
		noWait, _ := cmd.Flags().GetBool("no-wait")
		project, err := requireProject()
		if err != nil {
			return err
		}
		c := getClient()
		if force && !yes {
			ok, err := confirm(
				"Force-restart this project's runner?",
				"Every runner pod is replaced now, and any job one of them is running is killed — GitHub does not re-offer a job whose runner died.",
				"Yes, kill running jobs")
			if err != nil || !ok {
				return err
			}
		}
		runner, err := c.RestartRunner(context.Background(), project, client.RestartRunnerRequest{Force: force})
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
				{"Mode", restartMode(force)},
				{"", mutedStyle.Render("Carried out by the platform; `fpcloud runner show` reports it.")},
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
		done, err := c.WaitRunnerRestarted(context.Background(), project, 3*time.Second, progress)
		if err != nil {
			return err
		}
		if isStructured(outputFormat) {
			return renderData(done)
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				" Runner restarted: every runner pod and the listener are new.",
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
		return "waiting for the platform to replace the runner's pods and listener"
	}
	jobs := make([]string, 0, len(r.Restart.Waiting))
	for _, in := range r.Restart.Waiting {
		jobs = append(jobs, fmt.Sprintf("%q on %s (%s)", in.Job, in.Repository, time.Since(in.StartedAt).Round(time.Second)))
	}
	return fmt.Sprintf("draining: waiting for %d running job(s) to finish — %s", len(jobs), strings.Join(jobs, ", "))
}

// runnerBusy is "N of M" — pods executing a job beside the most that may run
// at once — because a bare count under any heading is ambiguous in a way the
// pair is not (fogpipe/cloud-workspace#146). M is what the org's ceiling
// admits when that is below the runner's own max.
func runnerBusy(r *client.Runner) string {
	most := r.MaxRunners
	if r.AdmittedRunners > 0 && r.AdmittedRunners < most {
		most = r.AdmittedRunners
	}
	return fmt.Sprintf("%d of %d", r.RunningRunners, most)
}

// runnerWaiting is the queue as GitHub sees it: jobs assigned to the runner
// that no pod has started. It is the whole diagnosis — an idle runner and one
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

// runnerUseNote says where the runner's label works, not only what it is.
// GitHub registers self-hosted runners per account, so a workflow in a
// repository outside the one the runner serves names a label nobody offers it
// and queues forever with no error on either side
// (fogpipe/cloud-workspace#305) — the label alone reads as if any repository
// could use it.
func runnerUseNote(r *client.Runner) string {
	scope := runnerScope(r)
	where := fmt.Sprintf("a github.com/%s repository", scope)
	outside := "A workflow in any other account's repository never sees this runner: its job queues forever."
	if scopeIsRepo(scope) {
		// A repository-scoped runner serves exactly one repository, so "any
		// repository in the account" is not merely imprecise here, it is wrong
		// (fogpipe/cloud-workspace#972).
		where = fmt.Sprintf("github.com/%s", scope)
		outside = "A workflow in any other repository never sees this runner: its job queues forever."
	}
	return fmt.Sprintf("  Use it from a workflow in %s with: runs-on: %s\n  %s",
		where, strings.Join(r.Labels, ", "), outside)
}

// scopeIsRepo reports whether a scope names one repository rather than a whole
// account. The slash is GitHub's own spelling for the difference.
func scopeIsRepo(scope string) bool {
	return strings.Contains(scope, "/")
}

// repoOutsideRunnerScope reports whether an owner/name repository is one the
// runner will never be offered. An account scope admits every repository in it;
// a repository scope admits exactly itself. GitHub logins are case-insensitive.
//
// Answered against the runner's own scope rather than the project's connection,
// because the connection exists only for the platform credential and says
// nothing about the other two (fogpipe/cloud-workspace#972).
func repoOutsideRunnerScope(repo, scope string) bool {
	if repo == "" || scope == "" {
		return false
	}
	if scopeIsRepo(scope) {
		return !strings.EqualFold(repo, scope)
	}
	owner, _, ok := strings.Cut(repo, "/")
	return ok && owner != "" && !strings.EqualFold(owner, scope)
}

// runnerScope renders where the runner registers: an account, whose every
// repository it serves, or one repository.
func runnerScope(r *client.Runner) string {
	return strings.TrimPrefix(strings.TrimPrefix(r.GitHubConfigURL, "https://github.com/"), "https://")
}

// runnerActivity says what the runner's pods are doing, not how many objects
// exist: a pod executing a job and one waiting for a slot the ceiling refuses
// were one "active" count, and the two call for opposite responses
// (fogpipe/cloud-workspace#120). A control plane that sends only the sum is
// rendered as the sum.
func runnerActivity(r *client.Runner) string {
	return runnerCounts(r.CurrentRunners, r.RunningRunners, r.PendingRunners)
}

// runnerCounts renders how many of a runner's pods are executing a job and how
// many exist without running one. Shared by `runner show` and by
// `project status`, so the two cannot come to different words for the same
// three numbers.
//
// A runner with 2 running and 2 pending reads identically to one with 4
// running under a single count, and the two call for opposite responses: pods
// executing jobs means the ceiling is working and a queue is expected, while
// pods pending means declared capacity is absent and the queue is the symptom
// (fogpipe/cloud-workspace#120). Both zero falls back to the sum, which is
// what a control plane that sends only the sum can support.
func runnerCounts(current, running, pending int) string {
	if running == 0 && pending == 0 {
		return fmt.Sprintf("%d active", current)
	}
	return fmt.Sprintf("%d running, %d pending", running, pending)
}

func runnerInfoRows(r *client.Runner) [][]string {
	scale := fmt.Sprintf("%d at once (%s)", r.MaxRunners, runnerActivity(r))
	if r.AdmittedRunners > 0 {
		scale = fmt.Sprintf("%d at once (%s, ceiling admits %d)", r.MaxRunners, runnerActivity(r), r.AdmittedRunners)
	}
	rows := [][]string{
		{"Serves", runnerScope(r)},
		{"runs-on", strings.Join(r.Labels, ", ")},
		{"Group", r.RunnerGroup},
		{"Size", r.Size},
		{"Max", scale},
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
	if len(r.Services) > 0 {
		rows = append(rows, []string{"Services", serviceCell(r.Services) + " — on 127.0.0.1 from your steps"})
	}
	switch r.Credential {
	case "platform":
		rows = append(rows, []string{"Credential", fmt.Sprintf("Fogpipe GitHub App (installation %s)", r.GitHubAppInstallationID)})
	case "app":
		rows = append(rows, []string{"Credential", fmt.Sprintf("your GitHub App %s (installation %s)", r.GitHubAppID, r.GitHubAppInstallationID)})
	default:
		rows = append(rows, []string{"Credential", "token"})
	}
	if r.Message != "" {
		rows = append(rows, []string{"Note", r.Message})
	}
	// Listed after the note rather than folded into it: a runner can have
	// several, and the count is the difference between one unlucky job and a
	// runner that is too small for the work being put through it.
	for _, p := range r.Problems {
		detail := p.Detail
		if p.Count > 1 {
			detail = fmt.Sprintf("%s (×%d)", detail, p.Count)
		}
		rows = append(rows, []string{"Problem", strings.TrimSpace(p.Reason + " — " + detail)})
	}
	// One row per live pod: what each is doing, from the platform itself —
	// never reconstructed from GitHub's API (fogpipe/cloud-workspace#129).
	for _, in := range r.Instances {
		rows = append(rows, []string{"Runner", runnerInstanceLine(in)})
	}
	return rows
}

// runnerInstanceLine renders one live pod: its state, how long it has run,
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

// runnerBuilderFromFlags reads the builder a create asked for, or nil for a
// runner that builds nothing. Naming a size implies the builder, so
// `--builder-memory 8Gi` alone does what it looks like; an unset size takes the
// platform's default for a builder, which is not the runner's own size
// (ADR-071).
func runnerBuilderFromFlags(cmd *cobra.Command) *client.RunnerBuilder {
	cpu, memory := mustString(cmd, "builder-cpu"), mustString(cmd, "builder-memory")
	if !mustBool(cmd, "builder") && cpu == "" && memory == "" {
		return nil
	}
	return &client.RunnerBuilder{CPU: cpu, Memory: memory}
}

// setRunnerBuilder fills in what an update said about the builder, and leaves
// the patch alone when it said nothing. Silence is not "remove it": a runner
// updated for its size keeps the builder it has.
//
// The field replaces the builder rather than patching it, so a size the command
// did not name is read back off the runner rather than dropped —
// `--builder-memory 8Gi` changes the memory and leaves the CPU where the tenant
// put it. That read is the only reason this needs the runner at all, so it
// happens only when a size was named and left half-stated.
func setRunnerBuilder(c *client.Client, project string, cmd *cobra.Command, req *client.UpdateRunnerRequest) error {
	if mustBool(cmd, "no-builder") {
		req.NoBuilder = true
		return nil
	}
	builder := runnerBuilderFromFlags(cmd)
	if builder == nil {
		return nil
	}
	if builder.CPU == "" || builder.Memory == "" {
		current, err := c.GetRunner(context.Background(), project)
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
	cmd.Flags().String("github-scope", "", "Where the runner registers: an organization (acme) or one repository (acme/backend). Only with --credential app or token; the Fogpipe App uses this project's connection")
	cmd.Flags().String("runner-group", "", "GitHub runner group to join (default Default)")
	cmd.Flags().String("size", "", "What one job gets: small (1 CPU, 2Gi), medium (2 CPU, 4Gi, the default) or large (4 CPU, 8Gi)")
	cmd.Flags().Int("max", 2, "Jobs run at once; a ceiling, not a cost")
	cmd.Flags().Bool("builder", false, "Run a rootless image builder alongside each job (sets BUILDKIT_HOST)")
	cmd.Flags().String("builder-cpu", "", "CPU size for the builder, e.g. 1 (implies --builder)")
	cmd.Flags().String("builder-memory", "", "Memory limit for the builder, e.g. 2Gi (implies --builder)")
	cmd.Flags().String("credential", "", "How the runner authenticates: platform (default, the Fogpipe GitHub App), app (your own), token")
	cmd.Flags().String("github-app-id", "", "GitHub App id (--credential app)")
	cmd.Flags().String("github-app-installation-id", "", "GitHub App installation id (--credential app)")
	cmd.Flags().String("github-app-private-key-file", "", "Path to your GitHub App private key, .pem (--credential app)")
	cmd.Flags().String("github-token", "", "Personal access token (--credential token)")
}

func init() {
	runnerSpecFlags(runnerCreateCmd)
	runnerSpecFlags(runnerUpdateCmd)
	runnerServiceFlags(runnerCreateCmd)
	runnerServiceFlags(runnerUpdateCmd)
	runnerUpdateCmd.Flags().Bool("no-services", false, "Remove every service container from the runner")
	runnerUpdateCmd.MarkFlagsMutuallyExclusive("no-services", "service")
	runnerUpdateCmd.Flags().Bool("no-builder", false, "Remove the runner's image builder")
	runnerUpdateCmd.MarkFlagsMutuallyExclusive("no-builder", "builder")
	runnerUpdateCmd.MarkFlagsMutuallyExclusive("no-builder", "builder-cpu")
	runnerUpdateCmd.MarkFlagsMutuallyExclusive("no-builder", "builder-memory")

	runnerRestartCmd.Flags().Bool("force", false, "Recycle at once, killing any job a runner pod is running (default: let running jobs finish)")
	runnerRestartCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt on --force")
	runnerRestartCmd.Flags().Bool("no-wait", false, "Return once the restart is accepted instead of waiting for it to finish")
	runnerCmd.AddCommand(runnerCreateCmd, runnerShowCmd, runnerUpdateCmd, runnerDeleteCmd, runnerRestartCmd)
	rootCmd.AddCommand(runnerCmd)
}
