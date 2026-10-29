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
	Long: `List every organization you hold a role in: on the organization itself,
on one of its projects, or on its billing. ROLE says which: your org role and
billing role, or "projects only" when you hold roles on some of its projects
and nothing on the organization.`,
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
		// REGISTRY is spend against the ceiling: what the org's projects were
		// last measured to hold beside what a push is refused above (ADR-128,
		// fogpipe/cloud-workspace#284). Never measured is said, not shown as 0.
		headers := []string{"", "ID", "NAME", "ROLE", "REGISTRY", "BACKUPS", "CREATED"}
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
				orgRoles(o),
				orgRegistrySpend(o),
				orgBackupSpend(o),
				o.CreatedAt.Format("2006-01-02 15:04"),
			})
		}
		render(headers, rows, orgs)
		return nil
	},
}

// orgRoles is what the caller holds in an org: its org role and billing role
// as the API names them, or "projects only" when it holds neither, which is
// the one other way an org is listed.
func orgRoles(o *client.Organization) string {
	var held []string
	for _, r := range []string{o.Role, o.BillingRole} {
		if r != "" {
			held = append(held, r)
		}
	}
	if len(held) == 0 {
		return "projects only"
	}
	return strings.Join(held, ", ")
}

var orgMembersCmd = &cobra.Command{
	Use:   "members [org]",
	Short: "List everyone who can reach an organization, with every role each holds in it",
	Long: `List everyone who can reach an organization: whoever holds a role on it, on
one of its projects, or on one resource in it.

ORG ROLES are the roles held on the organization itself, which reach every
project in it. OTHER ROLES are the roles held below it, each with what it is
held on.

STATUS says how far each person has got to signing in: active (signed in),
invited (an account exists and its setup mail was sent), not_provisioned (no
account yet, so no mail either), or unchecked (not read by the provisioning
pass yet, which runs every few minutes).

A person is invited by granting them a role, on the organization or on
anything in it:

  fpcloud iam grant --member user:ana@example.com --role viewer --on organization
  fpcloud iam grant --member user:ana@example.com --role editor --project web`,
	Args: cobra.MaximumNArgs(1),
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

		headers := []string{"EMAIL", "NAME", "ORG ROLES", "OTHER ROLES", "STATUS"}
		var rows [][]string
		for _, m := range members {
			rows = append(rows, []string{
				m.Email,
				m.UserName,
				renderRoles(m.Roles),
				memberHoldings(m.Holdings),
				renderStatus(m.Status),
			})
		}
		render(headers, rows, members)
		return nil
	},
}

var orgRemoveCmd = &cobra.Command{
	Use:   "remove <email|user-id>",
	Short: "Remove a person from everything they hold in an organization",
	Long: `Remove a person from an organization: every role they hold on it, on its
projects and on the resources in them, billing roles included, in one step.
An invitation nobody has redeemed yet is taken back the same way, by email.

The organization's last owner cannot be removed. To take back one role and
leave the rest, revoke that binding with 'fpcloud iam revoke'.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}

		member := args[0]
		c := getClient()
		withSpinner("Removing member...", func() {
			err = c.RemoveOrgMember(cmd.Context(), orgID, member)
		})
		if err != nil {
			return err
		}

		fmt.Println(successBox.Render(fmt.Sprintf("Removed %s and every role they held in the organization.", member)))
		return nil
	},
}

// resolveOrgID determines the organization a command should act on and returns
// its id. An explicit --org — or the current org from `org switch`, which is the
// persistent flag's default — wins. With nothing set it falls back to the
// caller's only organization, and refuses when there are several: picking the
// first would act on an arbitrary org rather than the one meant.
func resolveOrgID(cmd *cobra.Command) (string, error) {
	return resolveCurrentOrgID(cmd.Context())
}

// resolveCurrentOrgID is resolveOrgID for a caller holding a context rather
// than the command.
func resolveCurrentOrgID(ctx context.Context) (string, error) {
	if org := rootCmd.Flag("org").Value.String(); org != "" {
		return resolveOrgRef(ctx, org)
	}
	orgs, err := getClient().ListOrgs(ctx)
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

// renderRoles prints the roles a member holds on the organization, the owner
// role marked, or says there are none.
func renderRoles(roles []string) string {
	if len(roles) == 0 {
		return mutedStyle.Render("none")
	}
	out := make([]string, len(roles))
	for i, r := range roles {
		if r == "owner" {
			out[i] = titleStyle.Render(r)
		} else {
			out[i] = r
		}
	}
	return strings.Join(out, ", ")
}

// memberHoldings prints each role a member holds below the organization with
// what it is held on: "editor on demo", "secret.reader on secret db-key (demo)".
func memberHoldings(holdings []client.OrgMemberHolding) string {
	out := make([]string, len(holdings))
	for i, h := range holdings {
		switch {
		case h.ResourceType == "project":
			out[i] = h.Role + " on " + h.Project
		case h.Project != "":
			out[i] = h.Role + " on " + h.ResourceType + " " + h.ResourceName + " (" + h.Project + ")"
		default:
			out[i] = h.Role + " on " + h.ResourceType + " " + h.ResourceName
		}
	}
	return strings.Join(out, ", ")
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

	orgRenameCmd.Flags().String("name", "", "The organization's new readable name")
	orgCmd.AddCommand(orgRenameCmd)
	orgCmd.AddCommand(orgListCmd)
	orgCmd.AddCommand(orgSwitchCmd)
	orgCmd.AddCommand(orgMembersCmd)
	orgCmd.AddCommand(orgRemoveCmd)

	rootCmd.AddCommand(orgCmd)
}

// orgBackupSpend is orgRegistrySpend for the managed-backup axis (ADR-177).
// Separate rather than parameterised: the two read different fields off the
// same struct, and a shared helper taking four accessors is longer than saying
// it twice.
func orgBackupSpend(o *client.Organization) string {
	if o.MaxBackupStorage == "" {
		return ""
	}
	if o.BackupMeasuredAt.IsZero() {
		return mutedStyle.Render("never measured") + "/" + o.MaxBackupStorage
	}
	return humanizeSize(o.UsedBackupBytes) + "/" + o.MaxBackupStorage
}

func orgRegistrySpend(o *client.Organization) string {
	if o.MaxRegistryStorage == "" {
		return ""
	}
	if o.RegistryMeasuredAt.IsZero() {
		return mutedStyle.Render("never measured") + "/" + o.MaxRegistryStorage
	}
	return humanizeSize(o.UsedRegistryBytes) + "/" + o.MaxRegistryStorage
}
