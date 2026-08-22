package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
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
when it ends, so an idle pool costs nothing. A pool serves every repository in
the GitHub account this project is connected to.

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
		fmt.Println(mutedStyle.Render(fmt.Sprintf("  Use it from a workflow with: runs-on: %s", strings.Join(runner.Labels, ", "))))
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
				strconv.Itoa(r.CurrentRunners),
				renderStatus(r.Status),
			}
		}
		render([]string{"NAME", "SERVES", "SCALE", "ACTIVE", "STATUS"}, rows, runners)
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

		runner, err := c.UpdateRunner(context.Background(), id, req)
		if err != nil {
			return err
		}
		// The builder is its own call, so it is only made when the command said
		// something about one — otherwise every unrelated update would remove it.
		if builder, said := runnerBuilderChange(cmd, runner.Builder); said {
			runner, err = c.UpdateRunnerBuilder(context.Background(), id, builder)
			if err != nil {
				return err
			}
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

// runnerScope renders the GitHub account the pool serves — every repository in
// it. Derived from the project's connection, never typed.
func runnerScope(r *client.Runner) string {
	return strings.TrimPrefix(strings.TrimPrefix(r.GitHubConfigURL, "https://github.com/"), "https://")
}

func runnerInfoRows(r *client.Runner) [][]string {
	rows := [][]string{
		{"Name", r.Name},
		{"Serves", runnerScope(r)},
		{"runs-on", strings.Join(r.Labels, ", ")},
		{"Group", r.RunnerGroup},
		{"Scale", fmt.Sprintf("%d-%d (%d active)", r.MinRunners, r.MaxRunners, r.CurrentRunners)},
		{"Status", renderStatus(r.Status)},
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

// runnerBuilderChange reports what an update said about the builder, and whether
// it said anything at all. Silence is not "remove it": a pool updated for its
// display name keeps the builder it has.
//
// The endpoint replaces the builder rather than patching it, so a size the
// command did not name is carried over from current rather than dropped —
// `--builder-memory 8Gi` changes the memory and leaves the CPU where the tenant
// put it.
func runnerBuilderChange(cmd *cobra.Command, current *client.RunnerBuilder) (*client.RunnerBuilder, bool) {
	if mustBool(cmd, "no-builder") {
		return nil, true
	}
	builder := runnerBuilderFromFlags(cmd)
	if builder == nil {
		return nil, false
	}
	if current != nil {
		if builder.CPU == "" {
			builder.CPU = current.CPU
		}
		if builder.Memory == "" {
			builder.Memory = current.Memory
		}
	}
	return builder, true
}

// runnerSpecFlags are the knobs shared by create and update.
func runnerSpecFlags(cmd *cobra.Command) {
	cmd.Flags().String("github-account", "", "GitHub account the runner serves (only with --credential app or token; the Fogpipe App uses this project's connection)")
	cmd.Flags().String("runner-group", "", "GitHub runner group to join (default Default)")
	cmd.Flags().Int("min", 0, "Runners kept idle and ready (default 0, scale to zero)")
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

	runnerCmd.AddCommand(runnerCreateCmd, runnerListCmd, runnerShowCmd, runnerUpdateCmd, runnerDeleteCmd)
	rootCmd.AddCommand(runnerCmd)
}
