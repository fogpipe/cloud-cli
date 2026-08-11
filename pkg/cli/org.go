package cli

import (
	"context"
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

var orgCmd = &cobra.Command{
	Use:     "org",
	Aliases: []string{"orgs"},
	Short:   "Manage organizations and members",
}

var orgListCmd = &cobra.Command{
	Use:   "list",
	Short: "List organizations you belong to",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		var orgs []*client.Organization
		var err error
		withSpinner("Fetching organizations...", func() {
			orgs, err = c.ListOrgs(cmd.Context())
		})
		if err != nil {
			return err
		}

		current := rootCmd.Flag("org").Value.String()
		headers := []string{"", "NAME", "SHORT ID", "DISPLAY NAME", "CREATED"}
		var rows [][]string
		for _, o := range orgs {
			marker := " "
			if current != "" && (current == o.ID || current == o.Name) {
				marker = "*"
			}
			rows = append(rows, []string{
				marker,
				o.Name,
				o.ShortID,
				o.DisplayName,
				o.CreatedAt.Format("2006-01-02 15:04"),
			})
		}
		render(headers, rows, orgs)
		return nil
	},
}

var orgMembersCmd = &cobra.Command{
	Use:   "members [org-id]",
	Short: "List members of an organization",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var orgID string
		var err error
		if len(args) > 0 {
			orgID, err = resolveOrgRef(cmd.Context(), args[0])
		} else {
			orgID, err = resolveOrgID(cmd)
		}
		if err != nil {
			return err
		}
		c := getClient()
		var members []*client.OrgMember
		withSpinner("Fetching members...", func() {
			members, err = c.ListOrgMembers(cmd.Context(), orgID)
		})
		if err != nil {
			return err
		}

		headers := []string{"USER ID", "EMAIL", "NAME", "ROLE", "STATUS"}
		var rows [][]string
		for _, m := range members {
			rows = append(rows, []string{
				m.UserID,
				m.UserEmail,
				m.UserName,
				renderRole(m.Role),
				renderStatus(m.Status),
			})
		}
		render(headers, rows, members)
		return nil
	},
}

var orgInviteCmd = &cobra.Command{
	Use:   "invite <email>",
	Short: "Invite a user to your organization",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}

		role, _ := cmd.Flags().GetString("role")
		if role == "" {
			role = "viewer"
		}

		email := args[0]
		c := getClient()
		var member *client.OrgMember
		withSpinner("Inviting member...", func() {
			member, err = c.InviteOrgMember(cmd.Context(), orgID, email, role)
		})
		if err != nil {
			return err
		}

		statusLabel := "Invited (active)"
		if member.Status == "pending" {
			statusLabel = "Invited (pending registration)"
		}

		fmt.Println(renderInfoBox("Member Invited", [][]string{
			{"Email", email},
			{"Role", member.Role},
			{"Status", statusLabel},
		}))
		return nil
	},
}

var orgAddUserCmd = &cobra.Command{
	Use:   "add-user <email>",
	Short: "Provision a new user + API key in the organization (admin-only)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		role, _ := cmd.Flags().GetString("role")
		if role == "" {
			role = "viewer"
		}
		email := args[0]
		if name == "" {
			name = email
		}

		c := getClient()
		var resp *client.RegisterResponse
		withSpinner("Provisioning user...", func() {
			resp, err = c.ProvisionUser(cmd.Context(), orgID, email, name, role)
		})
		if err != nil {
			return err
		}

		fmt.Println(renderInfoBox("User Provisioned", [][]string{
			{"Email", resp.User.Email},
			{"User ID", resp.User.ID},
			{"Role", role},
			{"API Key", resp.APIKey},
		}))
		fmt.Println(mutedStyle.Render("Store the API key now — it is only shown once."))
		return nil
	},
}

var orgSetRoleCmd = &cobra.Command{
	Use:   "set-role <user-id>",
	Short: "Change a member's role",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}

		role, _ := cmd.Flags().GetString("role")
		if role == "" {
			return fmt.Errorf("--role is required (owner, editor, viewer)")
		}

		userID := args[0]
		c := getClient()
		withSpinner("Updating role...", func() {
			err = c.UpdateOrgMemberRole(cmd.Context(), orgID, userID, role)
		})
		if err != nil {
			return err
		}

		fmt.Println(successBox.Render(fmt.Sprintf("Updated role to %s for user %s.", role, userID)))
		return nil
	},
}

var orgRemoveCmd = &cobra.Command{
	Use:   "remove <user-id>",
	Short: "Remove a member from the organization",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}

		userID := args[0]
		c := getClient()
		withSpinner("Removing member...", func() {
			err = c.RemoveOrgMember(cmd.Context(), orgID, userID)
		})
		if err != nil {
			return err
		}

		fmt.Println(successBox.Render("Member removed from organization."))
		return nil
	},
}

// resolveOrgID determines the organization a command should act on and returns
// its id. An explicit --org — or the current org from `org use`, which is the
// persistent flag's default — wins; otherwise it falls back to the caller's
// first organization. It errors only when the user belongs to no organizations.
func resolveOrgID(cmd *cobra.Command) (string, error) {
	if org := rootCmd.Flag("org").Value.String(); org != "" {
		return resolveOrgRef(cmd.Context(), org)
	}
	orgs, err := getClient().ListOrgs(cmd.Context())
	if err != nil {
		return "", fmt.Errorf("could not determine org: %w", err)
	}
	if len(orgs) == 0 {
		return "", fmt.Errorf("no organization found; use --org or `fpcloud org use <org>`")
	}
	return orgs[0].ID, nil
}

// resolveOrgRef turns a user-supplied org reference (name or id) into an org id.
// A UUID is returned as-is; a plain name is matched against the caller's
// organizations, since the member/user endpoints only accept an org id.
func resolveOrgRef(ctx context.Context, ref string) (string, error) {
	if looksLikeUUID(ref) {
		return ref, nil
	}
	orgs, err := getClient().ListOrgs(ctx)
	if err != nil {
		return "", fmt.Errorf("could not resolve org %q: %w", ref, err)
	}
	for _, o := range orgs {
		if o.ID == ref || o.Name == ref {
			return o.ID, nil
		}
	}
	return "", fmt.Errorf("org %q not found", ref)
}

func renderRole(role string) string {
	switch role {
	case "owner":
		return titleStyle.Render(role)
	case "editor":
		return role
	default: // viewer
		return mutedStyle.Render(role)
	}
}

var orgUpdateCmd = &cobra.Command{
	Use:   "update <org>",
	Short: "Update an organization's display name",
	Long: "Change an organization's cosmetic display name. The frozen name and org id\n" +
		"(which anchor namespaces and the registry path) are unchanged.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		displayName, _ := cmd.Flags().GetString("display-name")
		if displayName == "" {
			return fmt.Errorf("--display-name is required")
		}
		c := getClient()
		var org *client.Organization
		var err error
		withSpinner("Updating organization...", func() {
			org, err = c.UpdateOrgDisplayName(cmd.Context(), args[0], displayName)
		})
		if err != nil {
			return err
		}
		fmt.Println(renderInfoBox("Organization Updated", [][]string{
			{"ID", mutedStyle.Render(org.ID)},
			{"Name", org.Name},
			{"Org ID", org.ShortID},
			{"Display Name", org.DisplayName},
		}))
		return nil
	},
}

var orgUseCmd = &cobra.Command{
	Use:   "use <org>",
	Short: "Set the current organization",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		// Validate the ref against the orgs the caller belongs to rather than
		// silently persisting a typo as context. GetOrg can't tell "unknown"
		// from "not a member" (both 403), so match the membership list.
		ref := args[0]
		orgs, err := getClient().ListOrgs(context.Background())
		if err != nil {
			return err
		}
		var match *client.Organization
		for _, o := range orgs {
			if o.Name == ref || o.ID == ref || o.ShortID == ref {
				match = o
				break
			}
		}
		if match == nil {
			return fmt.Errorf("no organization %q; run 'fpcloud org list'", ref)
		}
		cfg.CurrentOrg = ref
		// Cache the org's FKE entitlement so the `fke` command tree can be hidden
		// without a network call per invocation (best-effort; the server still
		// enforces).
		cfg.CurrentOrgFKE = match.FKEEnabled
		// Keep the org/project pair consistent: a current project set under the
		// previous org would otherwise linger and contradict the new org. Drop it
		// unless it also exists in the org we're switching to.
		// Keep the org/project pair consistent. Best-effort: if the project list
		// is unreachable, leave the project as-is rather than block the org switch.
		clearedProject := ""
		selectedProject := ""
		if projects, err := getClient().ListProjectsInOrg(context.Background(), match.ID); err == nil {
			inOrg := false
			for _, p := range projects {
				if p.Name == cfg.CurrentProject {
					inOrg = true
					break
				}
			}
			// A current project set under the previous org would otherwise linger
			// and contradict the new org, so drop it unless it also exists here.
			if cfg.CurrentProject != "" && !inOrg {
				clearedProject = cfg.CurrentProject
				cfg.CurrentProject = ""
			}
			// Convenience: when the new org has exactly one project the choice is
			// unambiguous, so adopt it instead of leaving the context project-less.
			if cfg.CurrentProject == "" && len(projects) == 1 {
				cfg.CurrentProject = projects[0].Name
				selectedProject = projects[0].Name
			}
		}
		if err := saveConfig(cfg); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Current organization set to %q.", args[0]),
		))
		switch {
		case selectedProject != "":
			fmt.Println(mutedStyle.Render(
				fmt.Sprintf("Current project set to %q (only project in this org).", selectedProject),
			))
		case clearedProject != "":
			fmt.Println(mutedStyle.Render(
				fmt.Sprintf("Cleared current project %q (not in this org); run 'fpcloud project use <name>'.", clearedProject),
			))
		}
		return nil
	},
}

func init() {
	// --org is inherited from the root persistent flag (default: current org from
	// `org use`); these commands must not shadow it with a local --org.
	orgInviteCmd.Flags().String("role", "viewer", "Role to assign (owner, editor, viewer)")
	orgAddUserCmd.Flags().String("name", "", "Display name (defaults to email)")
	orgAddUserCmd.Flags().String("role", "viewer", "Role to assign (owner, editor, viewer)")
	orgSetRoleCmd.Flags().String("role", "", "New role (owner, editor, viewer)")

	orgUpdateCmd.Flags().String("display-name", "", "New display name")
	orgCmd.AddCommand(orgUpdateCmd)
	orgCmd.AddCommand(orgListCmd)
	orgCmd.AddCommand(orgUseCmd)
	orgCmd.AddCommand(orgMembersCmd)
	orgCmd.AddCommand(orgInviteCmd)
	orgCmd.AddCommand(orgAddUserCmd)
	orgCmd.AddCommand(orgSetRoleCmd)
	orgCmd.AddCommand(orgRemoveCmd)

	rootCmd.AddCommand(orgCmd)
}
