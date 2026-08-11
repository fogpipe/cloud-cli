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

func init() {
	auditLogCmd.Flags().String("resource-type", "", "Filter by resource type (project, organization)")
	auditLogCmd.Flags().String("resource-id", "", "Filter by resource id")
	auditLogCmd.Flags().String("actor", "", "Filter by actor (email)")
	auditLogCmd.Flags().String("limit", "", "Max entries (default 100)")
	auditCmd.AddCommand(auditLogCmd)
	rootCmd.AddCommand(auditCmd)
}
