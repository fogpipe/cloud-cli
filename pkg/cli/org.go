package cli

import (
	"context"
	"fmt"
	"strings"

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
		headers := []string{"", "ID", "NAME", "CREATED"}
		var rows [][]string
		for _, o := range orgs {
			marker := " "
			if current != "" && (current == o.ID || current == o.ShortID || strings.EqualFold(current, o.DisplayName)) {
				marker = "*"
			}
			rows = append(rows, []string{
				marker,
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
	Use:   "set-role <user-id|email>",
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
	Use:   "remove <user-id|email>",
	Short: "Remove a member, or take back an invitation nobody has redeemed yet",
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
// its id. An explicit --org — or the current org from `org switch`, which is the
// persistent flag's default — wins. With nothing set it falls back to the
// caller's only organization, and refuses when there are several: picking the
// first would act on an arbitrary org rather than the one meant.
func resolveOrgID(cmd *cobra.Command) (string, error) {
	if org := rootCmd.Flag("org").Value.String(); org != "" {
		return resolveOrgRef(cmd.Context(), org)
	}
	orgs, err := getClient().ListOrgs(cmd.Context())
	if err != nil {
		return "", fmt.Errorf("could not determine org: %w", err)
	}
	if len(orgs) == 0 {
		return "", fmt.Errorf("no organization found; use --org or `fpcloud switch`")
	}
	if len(orgs) > 1 {
		names := make([]string, len(orgs))
		for i, o := range orgs {
			names[i] = o.ShortID
		}
		return "", fmt.Errorf("no organization selected and you belong to several (%s); run `fpcloud switch` or pass --org",
			strings.Join(names, ", "))
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
		if o.ID == ref || o.ShortID == ref || strings.EqualFold(o.DisplayName, ref) {
			return o.ID, nil
		}
	}
	return "", notFoundf("org %q not found", ref)
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

var orgRenameCmd = &cobra.Command{
	Use:   "rename <org>",
	Short: "Rename an organization",
	Long: "Change an organization's readable name.\n\n" +
		"Nothing is derived from it, so this moves no namespace, image path,\n" +
		"hostname or credential — it is a label change and nothing else. The org id\n" +
		"is frozen and is what those are built from.\n\n" +
		"A name is never handed to a second organization, including one you give up,\n" +
		"so a name you release stays yours and cannot start resolving to someone else.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		displayName, _ := cmd.Flags().GetString("name")
		if displayName == "" {
			return fmt.Errorf("--name is required")
		}
		c := getClient()
		var org *client.Organization
		var err error
		withSpinner("Renaming organization...", func() {
			org, err = c.UpdateOrgDisplayName(cmd.Context(), args[0], displayName)
		})
		if err != nil {
			return err
		}
		fmt.Println(renderInfoBox("Organization Renamed", [][]string{
			{"ID", org.ShortID},
			{"Name", org.DisplayName},
		}))
		return nil
	},
}

var orgSwitchCmd = &cobra.Command{
	Use:   "switch [org]",
	Short: "Switch the current organization",
	Long: `Point the CLI at an organization, asking which when not told.

The current project is cleared: a project belongs to exactly one organization,
so one chosen under the previous org would contradict the new one. Use
` + "`fpcloud switch`" + ` to choose an organization and a project together.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		ref := ""
		if len(args) > 0 {
			ref = args[0]
		}
		org, err := selectOrg(cmd.Context(), ref)
		if err != nil {
			return err
		}
		if org == nil {
			fmt.Println(mutedStyle.Render("Aborted."))
			return nil
		}
		applyOrg(cfg, org)
		cleared := cfg.CurrentProject
		cfg.CurrentProject = ""
		if err := saveConfig(cfg); err != nil {
			return err
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Current organization set to %q.", org.ShortID),
		))
		if cleared != "" {
			fmt.Println(mutedStyle.Render(
				fmt.Sprintf("Cleared current project %q; run 'fpcloud project switch'.", cleared),
			))
		}
		return nil
	},
}

func init() {
	// --org is inherited from the root persistent flag (default: current org from
	// `org switch`); these commands must not shadow it with a local --org.
	orgInviteCmd.Flags().String("role", "viewer", "Role to assign (owner, editor, viewer)")
	orgAddUserCmd.Flags().String("name", "", "Display name (defaults to email)")
	orgAddUserCmd.Flags().String("role", "viewer", "Role to assign (owner, editor, viewer)")
	orgSetRoleCmd.Flags().String("role", "", "New role (owner, editor, viewer)")

	orgRenameCmd.Flags().String("name", "", "The organization's new readable name")
	orgCmd.AddCommand(orgRenameCmd)
	orgCmd.AddCommand(orgListCmd)
	orgCmd.AddCommand(orgSwitchCmd)
	orgCmd.AddCommand(orgMembersCmd)
	orgCmd.AddCommand(orgInviteCmd)
	orgCmd.AddCommand(orgAddUserCmd)
	orgCmd.AddCommand(orgSetRoleCmd)
	orgCmd.AddCommand(orgRemoveCmd)

	rootCmd.AddCommand(orgCmd)
}
