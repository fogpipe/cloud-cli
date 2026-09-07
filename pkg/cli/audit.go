package cli

import (
	"net/url"

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
		q := url.Values{}
		if v, _ := cmd.Flags().GetString("resource-type"); v != "" {
			q.Set("resource_type", v)
		}
		if v, _ := cmd.Flags().GetString("resource-id"); v != "" {
			q.Set("resource_id", v)
		}
		if v, _ := cmd.Flags().GetString("actor"); v != "" {
			q.Set("actor", v)
		}
		if v, _ := cmd.Flags().GetString("limit"); v != "" {
			q.Set("limit", v)
		}

		c := getClient()
		var entries []*client.AuditEntry
		var err error
		withSpinner("Fetching audit log...", func() {
			entries, err = c.ListAudit(cmd.Context(), q.Encode())
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
	auditCmd.AddCommand(auditLogCmd)
	auditCmd.AddCommand(auditStatementsCmd)
	rootCmd.AddCommand(auditCmd)
}
