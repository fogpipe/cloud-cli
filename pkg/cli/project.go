package cli

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	Use:     "project",
	Aliases: []string{"projects"},
	Short:   "Manage projects",
}

var projectCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new project",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		if name == "" {
			if err := requirePrompt("give the project a name: fpcloud project create <name>"); err != nil {
				return err
			}
			form := huh.NewForm(
				huh.NewGroup(
					huh.NewInput().Title("Project name").Value(&name).Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("name is required")
						}
						return nil
					}),
				),
			)
			if err := form.Run(); err != nil {
				return err
			}
		}

		c := getClient()
		outputFormat := rootCmd.Flag("output").Value.String()

		var project *client.Project
		var err error
		egress, _ := cmd.Flags().GetString("egress")
		orgID, _ := cmd.Flags().GetString("org")
		req := client.CreateProjectRequest{
			Name:   name,
			Egress: egress,
		}
		action := func() {
			if orgID != "" {
				project, err = c.CreateProjectInOrg(context.Background(), orgID, req)
			} else {
				project, err = c.CreateProject(context.Background(), req)
			}
		}

		if !isStructured(outputFormat) {
			withSpinner("Creating project...", action)
		} else {
			action()
		}
		if err != nil {
			return err
		}

		setAsCurrent, _ := cmd.Flags().GetBool("use")
		if setAsCurrent {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			cfg.CurrentProject = project.Name
			if err := saveConfig(cfg); err != nil {
				return err
			}
		}

		if isStructured(outputFormat) {
			return renderData(project)
		}

		fmt.Println(renderInfoBox("Project Created", [][]string{
			{"ID", mutedStyle.Render(project.ID)},
			{"Name", project.Name},
			{"Display Name", project.DisplayName},
			{"Egress", egressLabel(project.Egress)},
			{"Caps", fmt.Sprintf("%s CPU / %s mem / %d pods / %s disk", project.MaxCPU, project.MaxMemory, project.MaxPods, project.MaxStorage)},
			{"Created", project.CreatedAt.Format("2006-01-02 15:04:05")},
		}))
		return nil
	},
}

var projectListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all projects",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c := getClient()

		// Scope to the selected org (--org / `org use`), falling back to the
		// caller's default org. To see another org's projects, switch orgs.
		orgRef := rootCmd.Flag("org").Value.String()
		orgName := ""
		if orgRef == "" {
			me, err := c.GetMe(ctx)
			if err != nil {
				return err
			}
			if me.Organization == nil {
				return fmt.Errorf("no organization selected; run 'fpcloud org use <org>' or pass --org")
			}
			orgRef = me.Organization.ID
			orgName = me.Organization.Name
		}

		projects, err := c.ListProjectsInOrg(ctx, orgRef)
		if err != nil {
			return err
		}

		// Show the scoped org once as context rather than on every row.
		if !isStructured(rootCmd.Flag("output").Value.String()) {
			if orgName == "" {
				if org, gerr := c.GetOrg(ctx, orgRef); gerr == nil {
					orgName = org.Name
				} else {
					orgName = orgRef
				}
			}
			fmt.Println(mutedStyle.Render("Organization: " + orgName))
		}

		// Group platform projects last, preserving the API's order within each group.
		sort.SliceStable(projects, func(i, j int) bool {
			return !projects[i].IsPlatform && projects[j].IsPlatform
		})

		// The APPS column is a table-only concern; fetch per-project apps
		// concurrently so we don't serialize N round-trips. With --apps we show
		// the app names, otherwise just the count. Structured output returns the
		// raw project objects untouched.
		showNames, _ := cmd.Flags().GetBool("apps")
		appCells := make([]string, len(projects))
		if !isStructured(rootCmd.Flag("output").Value.String()) {
			var wg sync.WaitGroup
			for i, p := range projects {
				wg.Add(1)
				go func(i int, id string) {
					defer wg.Done()
					apps, err := c.ListApps(ctx, id)
					if err != nil {
						appCells[i] = "?"
						return
					}
					if showNames {
						names := make([]string, len(apps))
						for j, a := range apps {
							names[j] = a.Name
						}
						appCells[i] = strings.Join(names, "\n")
						if len(apps) == 0 {
							appCells[i] = mutedStyle.Render("—")
						}
						return
					}
					appCells[i] = strconv.Itoa(len(apps))
				}(i, p.ID)
			}
			wg.Wait()
		}

		current := rootCmd.Flag("project").Value.String()
		hasPlatform := false
		rows := make([][]string, len(projects))
		for i, p := range projects {
			marker := " "
			if current != "" && (current == p.ID || current == p.Name) {
				marker = "*"
			}
			name := p.Name
			if p.IsPlatform {
				name += " " + mutedStyle.Render("🔒")
				hasPlatform = true
			}
			rows[i] = []string{marker, name, appCells[i], egressLabel(p.Egress), p.CreatedAt.Format("2006-01-02 15:04:05")}
		}
		render([]string{"", "NAME", "APPS", "EGRESS", "CREATED"}, rows, projects)
		if hasPlatform && !isStructured(rootCmd.Flag("output").Value.String()) {
			fmt.Println(mutedStyle.Render("🔒 platform project — cannot be deleted"))
		}
		return nil
	},
}

// egressLabel renders a project's egress mode in a human-friendly form.
func egressLabel(e string) string {
	switch e {
	case "all":
		return "all (open)"
	case "https":
		return "https (443 only)"
	case "restricted", "":
		return "restricted"
	default:
		return e
	}
}

var projectGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show project details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		project, err := c.GetProject(context.Background(), args[0])
		if err != nil {
			return err
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if isStructured(outputFormat) {
			return renderData(project)
		}

		fmt.Println(renderInfoBox("Project Details", [][]string{
			{"ID", mutedStyle.Render(project.ID)},
			{"Name", project.Name},
			{"Display Name", project.DisplayName},
			{"Egress", egressLabel(project.Egress)},
			{"Caps", fmt.Sprintf("%s CPU / %s mem / %d pods / %s disk", project.MaxCPU, project.MaxMemory, project.MaxPods, project.MaxStorage)},
			{"Created", project.CreatedAt.Format("2006-01-02 15:04:05")},
			{"Updated", project.UpdatedAt.Format("2006-01-02 15:04:05")},
		}))
		return nil
	},
}

var projectUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update a project's display name or egress mode",
	Long: `Update a project in place.

  --display-name  Change the project's cosmetic display name. The frozen name
                  (which anchors the namespace and registry path) is untouched.
  --egress        Egress policy: 'restricted', 'https' (443 only), or 'all' (open).

Resource caps (ADR-035) are set by the platform operator, not here.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		egress, _ := cmd.Flags().GetString("egress")
		displayName, _ := cmd.Flags().GetString("display-name")
		if displayName == "" && egress == "" {
			return fmt.Errorf("one of --display-name or --egress (restricted, https, all) is required")
		}

		c := getClient()
		var project *client.Project
		var err error
		action := func() {
			if displayName != "" {
				project, err = c.UpdateProjectDisplayName(context.Background(), args[0], displayName)
			} else {
				project, err = c.UpdateProjectEgress(context.Background(), args[0], egress)
			}
		}

		outputFormat := rootCmd.Flag("output").Value.String()
		if !isStructured(outputFormat) {
			withSpinner("Updating project...", action)
		} else {
			action()
		}
		if err != nil {
			return err
		}

		if isStructured(outputFormat) {
			return renderData(project)
		}

		fmt.Println(renderInfoBox("Project Updated", [][]string{
			{"Name", project.Name},
			{"Display Name", project.DisplayName},
			{"Egress", egressLabel(project.Egress)},
			{"Caps", fmt.Sprintf("%s CPU / %s mem / %d pods / %s disk", project.MaxCPU, project.MaxMemory, project.MaxPods, project.MaxStorage)},
		}))
		return nil
	},
}

var projectDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			ok, err := confirm(
				fmt.Sprintf("Delete project %q?", args[0]),
				"Deletes its apps, databases, and its buckets INCLUDING every object in them\n"+
					"(website files included), plus the backups of its databases.\n"+
					"This action cannot be undone.",
				"Yes, delete")
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}

		c := getClient()
		if _, err := c.DeleteProject(context.Background(), args[0]); err != nil {
			return err
		}

		// The API accepts the delete and tears the project down behind it, so
		// this waits for the teardown rather than announcing a deletion that is
		// still running (#865). --no-wait returns as soon as it is accepted.
		if noWait, _ := cmd.Flags().GetBool("no-wait"); noWait {
			fmt.Println(successBox.Render(
				lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
					fmt.Sprintf(" Project %q is being deleted.", args[0]),
			))
			return nil
		}

		var waitErr error
		withSpinner("Deleting project...", func() {
			waitErr = c.WaitProjectDeleted(context.Background(), args[0], 2*time.Second)
		})
		if waitErr != nil {
			return waitErr
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Project %q deleted.", args[0]),
		))
		return nil
	},
}

var projectMoveCmd = &cobra.Command{
	Use:   "move <name>",
	Short: "Re-home a project into its org-prefixed namespace",
	Long: "Reconciles the project into <org-short-id>-<name> and redeploys its apps.\n" +
		"Stateful data (databases, PVCs) is NOT migrated — back it up and restore\n" +
		"separately, then re-run with --force.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			ok, err := confirm(
				fmt.Sprintf("Move project %q to its org-prefixed namespace?", args[0]),
				"Recreates the namespace and redeploys apps (brief downtime).",
				"Yes, move")
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		c := getClient()
		res, err := c.MoveProject(context.Background(), args[0], force)
		if err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Project %q moved to namespace %q.", res.Project.Name, res.Project.Namespace),
		))
		for _, w := range res.Warnings {
			fmt.Println(mutedStyle.Render("  ! " + w))
		}
		return nil
	},
}

var projectUseCmd = &cobra.Command{
	Use:   "use [name]",
	Short: "Set the current project (interactive picker if no name given)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c := getClient()

		// Scope to the selected org (--org / `org use`), falling back to
		// the caller's default org — same as `project list`.
		orgRef := rootCmd.Flag("org").Value.String()
		if orgRef == "" {
			me, err := c.GetMe(ctx)
			if err != nil {
				return err
			}
			if me.Organization == nil {
				return fmt.Errorf("no organization selected; run 'fpcloud org use <org>' or pass --org")
			}
			orgRef = me.Organization.ID
		}

		projects, err := c.ListProjectsInOrg(ctx, orgRef)
		if err != nil {
			return err
		}
		if len(projects) == 0 {
			fmt.Println(mutedStyle.Render("No projects found."))
			return nil
		}

		name := ""
		if len(args) > 0 {
			// Validate the requested project exists in this org rather than
			// silently persisting a typo that only fails on the next command.
			name = args[0]
			found := false
			for _, p := range projects {
				if p.Name == name {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("no project %q in this organization; run 'fpcloud project list'", name)
			}
		} else {
			name, err = pickProject(projects)
			if err != nil {
				return err
			}
			if name == "" {
				fmt.Println(mutedStyle.Render("Aborted."))
				return nil
			}
		}

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		cfg.CurrentProject = name
		if err := saveConfig(cfg); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Current project set to %q.", name),
		))
		return nil
	},
}

// pickProject prompts the user to choose a project, returning its name (or ""
// if cancelled). It uses fzf when available, falling back to a huh select.
func pickProject(projects []*client.Project) (string, error) {
	// Before the fzf branch: fzf draws on the terminal too, so neither picker
	// has anything to draw on.
	if err := requirePrompt("name the project: fpcloud project use <name>"); err != nil {
		return "", err
	}
	if fzf, err := exec.LookPath("fzf"); err == nil {
		return pickProjectFzf(fzf, projects)
	}

	options := make([]huh.Option[string], len(projects))
	for i, p := range projects {
		label := p.Name
		if p.IsPlatform {
			label += " 🔒"
		}
		label += "  " + mutedStyle.Render(egressLabel(p.Egress))
		options[i] = huh.NewOption(label, p.Name)
	}
	var selected string
	err := huh.NewSelect[string]().
		Title("Select a project").
		Options(options...).
		Value(&selected).
		Run()
	if err != nil {
		if err == huh.ErrUserAborted {
			return "", nil
		}
		return "", err
	}
	return selected, nil
}

func pickProjectFzf(fzf string, projects []*client.Project) (string, error) {
	var input strings.Builder
	for _, p := range projects {
		// "<name>\t<egress>" — the name is the first tab-delimited field.
		eg := egressLabel(p.Egress)
		if p.IsPlatform {
			eg += " 🔒"
		}
		fmt.Fprintf(&input, "%s\t%s\n", p.Name, eg)
	}

	cmd := exec.Command(fzf,
		"--prompt=project> ",
		"--with-nth=1,2",
		"--delimiter=\t",
		"--height=40%",
		"--reverse",
		"--header=Select a project")
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = nil // fzf draws its UI on the terminal directly
	out, err := cmd.Output()
	if err != nil {
		// Exit code 130 = user cancelled (Esc/Ctrl-C); treat as no selection.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return "", nil
		}
		return "", err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", nil
	}
	return strings.SplitN(line, "\t", 2)[0], nil
}

func init() {
	projectCreateCmd.Flags().Bool("use", true, "Set as current project after creation (pass --use=false to skip)")
	projectCreateCmd.Flags().String("egress", "restricted", "Egress policy: 'restricted' (default), 'https' (443 only), or 'all' (open)")
	projectUpdateCmd.Flags().String("display-name", "", "New cosmetic display name (the frozen project name is unchanged)")
	projectUpdateCmd.Flags().String("egress", "", "Egress policy: 'restricted', 'https' (443 only), or 'all' (open)")
	projectListCmd.Flags().Bool("apps", false, "List each project's app names instead of just the count")
	projectDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	projectDeleteCmd.Flags().Bool("no-wait", false, "Return once the deletion is accepted, without waiting for the teardown")
	projectMoveCmd.Flags().Bool("force", false, "Proceed even if the project has stateful resources (data is not migrated)")
	projectMoveCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	projectCmd.AddCommand(projectCreateCmd, projectListCmd, projectGetCmd, projectStatusCmd, projectUpdateCmd, projectDeleteCmd, projectMoveCmd, projectUseCmd)
	rootCmd.AddCommand(projectCmd)
}
