package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

var switchCmd = &cobra.Command{
	Use:   "switch [org] [project]",
	Short: "Switch the current organization and project",
	Long: `Point the CLI at an organization and a project under it.

A project belongs to exactly one organization, so the two are one context and
this sets both. Given no arguments it asks, skipping either question when there
is only one answer:

  fpcloud switch                       # pick an org, then a project
  fpcloud switch rymdkraftverk         # that org, then pick a project
  fpcloud switch rymdkraftverk foobar  # both, no prompt

Both arguments complete on TAB. With no terminal to ask on, a missing argument
is an error naming the form to pass rather than a hang, so scripts state the
context they mean.

To move one axis alone, use ` + "`fpcloud org switch`" + ` or ` + "`fpcloud project switch`" + `.`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgRef := ""
		if len(args) > 0 {
			orgRef = args[0]
		}
		projectRef := ""
		if len(args) > 1 {
			projectRef = args[1]
		}

		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		org, err := selectOrg(cmd.Context(), orgRef)
		if err != nil {
			return err
		}
		if org == nil {
			fmt.Println(mutedStyle.Render("Aborted."))
			return nil
		}
		applyOrg(cfg, org)

		project, ok, err := selectProject(cmd.Context(), org, projectRef)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println(mutedStyle.Render("Aborted."))
			return nil
		}
		cfg.CurrentProject = project

		if err := saveConfig(cfg); err != nil {
			return err
		}
		printContext(org, project)
		return nil
	},
}

// selectOrg turns a reference into an org, or asks for one when ref is empty.
// Returns nil when the user cancelled the picker.
func selectOrg(ctx context.Context, ref string) (*client.Organization, error) {
	orgs, err := getClient().ListOrgs(ctx)
	if err != nil {
		return nil, err
	}
	if len(orgs) == 0 {
		return nil, fmt.Errorf("you belong to no organization")
	}
	if ref != "" {
		// Validate against the orgs the caller belongs to rather than silently
		// persisting a typo as context. GetOrg can't tell "unknown" from "not a
		// member" (both 403), so match the membership list.
		org := matchOrg(orgs, ref)
		if org == nil {
			return nil, fmt.Errorf("no organization %q; run 'fpcloud org list'", ref)
		}
		return org, nil
	}
	if len(orgs) == 1 {
		return orgs[0], nil
	}
	return pickOrg(orgs)
}

// selectProject resolves the project under org, asking when ref is empty.
// ok is false only when the user cancelled the picker — an org with no projects
// answers "" and true, since empty is the right context there.
func selectProject(ctx context.Context, org *client.Organization, ref string) (name string, ok bool, err error) {
	projects, err := getClient().ListProjectsInOrg(ctx, org.ID)
	if err != nil {
		return "", false, err
	}
	if ref != "" {
		for _, p := range projects {
			if p.Name == ref {
				return p.Name, true, nil
			}
		}
		return "", false, fmt.Errorf("no project %q in organization %q; run 'fpcloud project list'", ref, org.ShortID)
	}
	switch len(projects) {
	case 0:
		return "", true, nil
	case 1:
		return projects[0].Name, true, nil
	}
	picked, err := pickProject(projects)
	return picked, picked != "" && err == nil, err
}

// matchOrg finds the org a reference names — its uuid, its frozen short id, or
// its readable name.
func matchOrg(orgs []*client.Organization, ref string) *client.Organization {
	for _, o := range orgs {
		if o.ID == ref || o.ShortID == ref || strings.EqualFold(o.DisplayName, ref) {
			return o
		}
	}
	return nil
}

// applyOrg writes the org into the config. It stores the frozen short id, never
// the reference as typed: a readable name is mutable (ADR-094), so a config
// holding one stops resolving the moment the org is renamed.
func applyOrg(cfg *Config, org *client.Organization) {
	cfg.CurrentOrg = org.ShortID
	// Cache the org's FKE entitlement so the `fke` command tree can be hidden
	// without a network call per invocation (best-effort; the server still
	// enforces).
	cfg.CurrentOrgFKE = org.FKEEnabled
}

// printContext reports the pair a switch settled on.
func printContext(org *client.Organization, project string) {
	fmt.Println(successBox.Render(
		lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
			fmt.Sprintf(" Switched to %s/%s.", org.ShortID, orDefault(project, "(no project)")),
	))
	if project == "" {
		fmt.Println(mutedStyle.Render(
			fmt.Sprintf("Organization %q has no projects; run 'fpcloud project create <name>'.", org.ShortID),
		))
	}
}

// pickOrg prompts the user to choose an organization, returning nil if
// cancelled. It uses fzf when available, falling back to a huh select.
func pickOrg(orgs []*client.Organization) (*client.Organization, error) {
	// Before the fzf branch: fzf draws on the terminal too, so neither picker
	// has anything to draw on.
	if err := requirePrompt("name the organization: fpcloud switch <org>"); err != nil {
		return nil, err
	}
	if fzf, err := exec.LookPath("fzf"); err == nil {
		return pickOrgFzf(fzf, orgs)
	}

	width := 0
	for _, o := range orgs {
		if len(o.ShortID) > width {
			width = len(o.ShortID)
		}
	}
	options := make([]huh.Option[string], len(orgs))
	for i, o := range orgs {
		options[i] = huh.NewOption(fmt.Sprintf("%-*s  %s", width, o.ShortID, mutedStyle.Render(o.DisplayName)), o.ShortID)
	}
	var selected string
	if err := huh.NewSelect[string]().
		Title("Select an organization").
		Options(options...).
		Value(&selected).
		Run(); err != nil {
		if err == huh.ErrUserAborted {
			return nil, nil
		}
		return nil, err
	}
	return matchOrg(orgs, selected), nil
}

func pickOrgFzf(fzf string, orgs []*client.Organization) (*client.Organization, error) {
	width := 0
	for _, o := range orgs {
		if len(o.ShortID) > width {
			width = len(o.ShortID)
		}
	}
	var input strings.Builder
	for _, o := range orgs {
		// "<short-id>\t<display-name>" — the id is the first tab-delimited field,
		// padded to the longest so the tab lands in one column.
		fmt.Fprintf(&input, "%-*s\t%s\n", width, o.ShortID, o.DisplayName)
	}

	cmd := exec.Command(fzf,
		"--prompt=org> ",
		"--with-nth=1,2",
		"--delimiter=\t",
		"--height=40%",
		"--reverse",
		"--header=Select an organization")
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = nil // fzf draws its UI on the terminal directly
	out, err := cmd.Output()
	if err != nil {
		// Exit code 130 = user cancelled (Esc/Ctrl-C); treat as no selection.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return nil, nil
		}
		return nil, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil, nil
	}
	return matchOrg(orgs, strings.SplitN(line, "\t", 2)[0]), nil
}

func init() {
	rootCmd.AddCommand(switchCmd)
}

// seedContext gives a fresh login a working context, asking the same questions
// `fpcloud switch` does. Best-effort in every direction: a caller with no
// terminal, no membership or no reachable control plane is pointed at the
// command instead, because failing here would fail a login that succeeded.
func seedContext(ctx context.Context) {
	cfg, err := loadConfig()
	if err != nil {
		return
	}
	hint := func() {
		fmt.Println(mutedStyle.Render("  Set your context with:      fpcloud switch"))
	}
	// A saved context belongs to whoever saved it. Signing in as someone else
	// must not inherit an org that identity cannot see: every command would
	// then fail with a 403 that reads like a permissions problem, on a context
	// the person never chose (fogpipe/cloud-workspace#103).
	if cfg.CurrentOrg != "" {
		orgs, err := getClient().ListOrgs(ctx)
		if err != nil {
			hint()
			return
		}
		if matchOrg(orgs, cfg.CurrentOrg) == nil {
			fmt.Println(mutedStyle.Render(fmt.Sprintf("  You are not a member of the saved context's org (%s); choosing again.", cfg.CurrentOrg)))
			cfg.CurrentOrg, cfg.CurrentProject, cfg.CurrentOrgFKE = "", "", false
			// Written now, not after a choice: a picker the person leaves must
			// leave no context behind, never the stale one.
			if err := saveConfig(cfg); err != nil {
				hint()
				return
			}
		}
	}
	if cfg.CurrentOrg != "" && cfg.CurrentProject != "" {
		return
	}
	org, err := selectOrg(ctx, "")
	if err != nil || org == nil {
		hint()
		return
	}
	applyOrg(cfg, org)
	// The org is known the moment it is chosen — for most people without a
	// question, since they belong to one — so it is written now. A project
	// picker the person leaves then leaves the org set and only the project
	// open, instead of throwing both away.
	if err := saveConfig(cfg); err != nil {
		hint()
		return
	}
	project, ok, err := selectProject(ctx, org, "")
	if err != nil || !ok {
		fmt.Println(mutedStyle.Render(fmt.Sprintf("  Organization %s; pick a project with:  fpcloud switch", org.ShortID)))
		return
	}
	cfg.CurrentProject = project
	if err := saveConfig(cfg); err != nil {
		hint()
		return
	}
	printContext(org, project)
}
