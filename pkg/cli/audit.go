package cli

import (
	"fmt"
	"strconv"

	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Inspect the audit log",
}

var auditLogCmd = &cobra.Command{
	Use:   "log",
	Short: "Show recent audit log entries (who did what, when)",
	RunE: func(cmd *cobra.Command, args []string) error {
		var q client.AuditQuery
		q.ResourceType, _ = cmd.Flags().GetString("resource-type")
		q.ResourceID, _ = cmd.Flags().GetString("resource-id")
		q.Actor, _ = cmd.Flags().GetString("actor")
		if v, _ := cmd.Flags().GetString("limit"); v != "" {
			limit, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("--limit: %q is not a whole number", v)
			}
			q.Limit = limit
		}

		c := getClient()
		// The log has three addresses: the organization's, which is every
		// entry in it and in any of its projects; one project's; and the
		// caller's own, wherever it was recorded. The first two are read by
		// permission on the scope, the third by being the person.
		scope, _ := cmd.Flags().GetString("scope")
		var read func() ([]*client.AuditEntry, error)
		switch scope {
		case "", "org":
			orgID, err := resolveOrgID(cmd)
			if err != nil {
				return err
			}
			read = func() ([]*client.AuditEntry, error) { return c.OrgAudit(cmd.Context(), orgID, q) }
		case "project":
			ref, err := requireProject()
			if err != nil {
				return err
			}
			project, err := c.GetProject(cmd.Context(), ref)
			if err != nil {
				return err
			}
			read = func() ([]*client.AuditEntry, error) { return c.ProjectAudit(cmd.Context(), project.ID, q) }
		case "me":
			read = func() ([]*client.AuditEntry, error) { return c.MyAudit(cmd.Context(), q) }
		default:
			return fmt.Errorf("--scope must be org, project or me, got %q", scope)
		}
		var entries []*client.AuditEntry
		var err error
		withSpinner("Fetching audit log...", func() {
			entries, err = read()
		})
		if err != nil {
			return err
		}

		headers := []string{"TIME", "ACTOR", "ACTION", "RESOURCE"}
		rows := make([][]string, len(entries))
		for i, e := range entries {
			rows[i] = []string{
				e.Timestamp.Format("2006-01-02 15:04:05"),
				e.Actor,
				e.Action,
				e.ResourceType + ":" + e.ResourceID,
			}
		}
		render(headers, rows, entries)
		return nil
	},
}

// auditStatementsCmd is the other half of "who did what": the control-plane log
// answers who opened a session, and the database answers what they ran inside
// it (ADR-163).
var auditStatementsCmd = &cobra.Command{
	Use:   "statements <database>",
	Short: "Show what people ran through a database's tunnels",
	Long: `Show the statements run through ` + "`fpcloud db connect`" + ` on a database, with the
person who ran each one.

The database records them itself, under the name of the session role the
platform minted for whoever opened the tunnel — which is why they can be
attributed at all. Your application's own traffic is never recorded here: it
connects as your own role, and the audit filter is attached to the session
roles the platform mints.

Needs the pgaudit extension on the database:

  fpcloud db update mydb --extension pgaudit --cpu 500m --memory 1Gi --storage 10Gi

Entries live with your logs and reach back as far as they do (14 days). The
platform can say who connected for as long as the audit log exists, and what
they did for that window.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		since, _ := cmd.Flags().GetString("since")
		until, _ := cmd.Flags().GetString("until")
		limit, _ := cmd.Flags().GetInt("limit")

		c := getClient()
		id, err := resolveDatabaseID(c, args[0])
		if err != nil {
			return err
		}
		var entries []client.DatabaseAuditEntry
		withSpinner("Reading audit records...", func() {
			entries, err = c.DatabaseAuditLog(cmd.Context(), id, since, until, limit)
		})
		if err != nil {
			return err
		}

		headers := []string{"TIME", "WHO", "CLASS", "COMMAND", "OBJECT", "STATEMENT"}
		rows := make([][]string, len(entries))
		for i, e := range entries {
			who := e.Actor
			if who == "" {
				// The role rather than a blank: an entry nobody can be named for
				// is still an entry, and a blank reads as a statement nobody ran.
				who = e.Role
			}
			rows[i] = []string{
				e.At.Format("2006-01-02 15:04:05"),
				who,
				e.Class,
				e.Command,
				orDash(e.Object),
				e.Statement,
			}
		}
		render(headers, rows, entries)
		return nil
	},
}

func init() {
	auditLogCmd.Flags().String("resource-type", "", "Filter by resource type (project, organization)")
	auditLogCmd.Flags().String("resource-id", "", "Filter by resource id")
	auditLogCmd.Flags().String("actor", "", "Filter by actor (email)")
	auditStatementsCmd.Flags().String("since", "", "How far back: a duration ago (24h) or an RFC3339 timestamp (default 24h)")
	auditStatementsCmd.Flags().String("until", "", "The far end of the window, same forms")
	auditStatementsCmd.Flags().Int("limit", 0, "Most entries to return")
	auditLogCmd.Flags().String("limit", "", "Max entries (default 100)")
	auditLogCmd.Flags().String("scope", "org", "org: the organization's whole trail, every project included; project: the current project only; me: what you did, wherever you did it")
	auditCmd.AddCommand(auditLogCmd)
	auditCmd.AddCommand(auditStatementsCmd)
	rootCmd.AddCommand(auditCmd)
}
