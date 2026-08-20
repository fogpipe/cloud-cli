package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// Operator-only commands. Everything here calls /api/v1/admin/*, which is gated
// on administrate over the platform-operator org — a tenant gets 403, and the
// subtree is withheld at the public Ingress besides. It is a visible command
// group rather than a hidden one: what an ordinary account may not do is not a
// secret, and hiding it only makes staff guess at the spelling.
var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Operator-only commands (platform staff)",
	Long: `Commands for operating the platform itself.

Every command here requires administrate on the platform-operator organization.
An ordinary account is refused.`,
}

var adminAlertsCmd = &cobra.Command{
	Use:   "alerts",
	Short: "What fired, when, and for how long",
	Long: `Show the alert episodes Prometheus recorded over a window.

The #alerts channel is a stream, not a record: Alertmanager stores no history,
and Slack holds only what was delivered — never what was inhibited, discarded, or
fired before the channel existed. This reads Prometheus's ALERTS series through
the control plane, so it needs an fpcloud login and nothing else.

  fpcloud admin alerts                    the last 7 days
  fpcloud admin alerts --window 24h       a different window
  fpcloud admin alerts --name NodeDisk    alertname, case-insensitive substring
  fpcloud admin alerts -o json            every label, for a script

The window is bounded by the cluster's Prometheus retention. A window reaching
past it comes back as the part that still exists, and the header says which — an
empty answer means "not in what was kept", never "this has never fired".`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		window, _ := cmd.Flags().GetString("window")
		name, _ := cmd.Flags().GetString("name")

		c := getClient()
		var history *client.AlertHistory
		var err error
		withSpinner("Reading alert history...", func() {
			history, err = c.AlertHistory(cmd.Context(), window, name)
		})
		if err != nil {
			return err
		}

		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(history)
		}

		fmt.Println(alertHistoryHeader(history))
		if len(history.Episodes) == 0 {
			fmt.Println("(nothing fired)")
			return nil
		}
		headers := []string{"STARTED", "DURATION", "SEVERITY", "ALERT", "TARGET"}
		rows := make([][]string, 0, len(history.Episodes))
		for _, e := range history.Episodes {
			rows = append(rows, []string{
				e.StartedAt.Local().Format("2006-01-02 15:04"),
				alertDuration(e),
				e.Severity,
				e.Alert,
				alertScope(e),
			})
		}
		renderTable(headers, rows)
		return nil
	},
}

// alertHistoryHeader states the window actually observed and its resolution.
//
// Both matter to how the rows are read: the window because retention silently
// truncates it, and the step because an episode is only located to within one
// sampling interval.
func alertHistoryHeader(h *client.AlertHistory) string {
	return fmt.Sprintf("# %s → %s, sampled every %s",
		h.From.Local().Format("2006-01-02 15:04"),
		h.To.Local().Format("2006-01-02 15:04"),
		time.Duration(h.StepSeconds)*time.Second,
	)
}

// alertDuration renders how long an episode ran, marking one still open. A
// closed and an open episode of the same length are not the same fact.
func alertDuration(e client.AlertEpisode) string {
	d := e.EndedAt.Sub(e.StartedAt)
	if e.Firing {
		return shortDuration(d) + "+"
	}
	return shortDuration(d)
}

// alertScope names what the alert was about, namespace-qualified when it has
// one. An alert scoped to neither — a node, the cluster — renders empty rather
// than inventing a scope for it.
func alertScope(e client.AlertEpisode) string {
	parts := make([]string, 0, 2)
	for _, p := range []string{e.Namespace, e.Target} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}

func init() {
	adminAlertsCmd.Flags().String("window", "", "How far back to look: 30m, 24h, 7d, 2w (default 7d)")
	adminAlertsCmd.Flags().String("name", "", "Only alerts whose name contains this (case-insensitive)")
	adminCmd.AddCommand(adminAlertsCmd)
	rootCmd.AddCommand(adminCmd)
}
