package cli

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show metered resource usage",
	Long: `Show what your project consumed over a period.

Quantities only — core-hours of CPU, GiB-hours of memory and storage. Usage is
metered hourly and kept after the resource is deleted, so a period still reports
what a since-deleted app used.

  # this project, per app, over the last 30 days
  fpcloud usage

  # one app, day by day
  fpcloud usage --app api --group-by day

  # every workload's trend
  fpcloud usage --group-by day,app

  # every project in the org, last week
  fpcloud usage --org-wide --from 7d`,
	RunE: func(cmd *cobra.Command, args []string) error {
		q := url.Values{}

		for _, f := range []string{"from", "to"} {
			raw, _ := cmd.Flags().GetString(f)
			if raw == "" {
				continue
			}
			ts, err := parseTimeRef(raw)
			if err != nil {
				return fmt.Errorf("--%s: %w", f, err)
			}
			q.Set(f, ts.Format(time.RFC3339))
		}

		orgWide, _ := cmd.Flags().GetBool("org-wide")
		groupBy, _ := cmd.Flags().GetString("group-by")
		if groupBy == "" {
			// The useful default is one level down from the scope asked about:
			// which app in this project, or which project in this org.
			groupBy = "app"
			if orgWide {
				groupBy = "project"
			}
		}
		q.Set("group_by", groupBy)

		c := getClient()
		if ref, _ := cmd.Flags().GetString("app"); ref != "" {
			if orgWide {
				return fmt.Errorf("--app is scoped to a project; drop --org-wide")
			}
			appID, err := resolveAppID(c, ref)
			if err != nil {
				return err
			}
			q.Set("app_id", appID)
		}

		var entries []*client.UsageEntry
		var err error
		if orgWide {
			orgID, oerr := resolveOrgID(cmd)
			if oerr != nil {
				return oerr
			}
			withSpinner("Fetching usage...", func() {
				entries, err = c.ListOrgUsage(cmd.Context(), orgID, q.Encode())
			})
		} else {
			project, perr := requireProject()
			if perr != nil {
				return perr
			}
			withSpinner("Fetching usage...", func() {
				entries, err = c.ListProjectUsage(cmd.Context(), project, q.Encode())
			})
		}
		if err != nil {
			return err
		}

		headers, rows := usageRows(groupBy, entries)
		render(headers, rows, entries)
		return nil
	},
}

// usageRows lays a result set out for the axes it was grouped along: one
// leading column per axis, in the order day, project, app, so a workload's
// trend (--group-by day,app) reads top to bottom then left to right.
func usageRows(groupBy string, entries []*client.UsageEntry) ([]string, [][]string) {
	asked := map[string]bool{}
	for _, a := range strings.Split(groupBy, ",") {
		asked[strings.TrimSpace(a)] = true
	}
	type column struct {
		header string
		cell   func(e *client.UsageEntry) string
	}
	var lead []column
	if asked["day"] {
		lead = append(lead, column{"DAY", func(e *client.UsageEntry) string {
			if e.Day == nil {
				return ""
			}
			return e.Day.Format("2006-01-02")
		}})
	}
	if asked["project"] {
		lead = append(lead, column{"PROJECT", func(e *client.UsageEntry) string { return e.ProjectName }})
	}
	if asked["app"] {
		lead = append(lead, column{"APP", func(e *client.UsageEntry) string {
			if e.AppName == "" {
				// Usage the collector could attribute to the project but not to a
				// workload — a pod with no app label. Naming it keeps the column
				// total honest instead of hiding a blank row.
				return "(project)"
			}
			return e.AppName
		}})
	}

	headers := []string{"RESOURCE", "QUANTITY", "UNIT"}
	for i := len(lead) - 1; i >= 0; i-- {
		headers = append([]string{lead[i].header}, headers...)
	}
	rows := make([][]string, len(entries))
	for i, e := range entries {
		row := make([]string, 0, len(lead)+3)
		for _, c := range lead {
			row = append(row, c.cell(e))
		}
		rows[i] = append(row, e.ResourceType, formatQuantity(e.Quantity), e.Unit)
	}
	return headers, rows
}

// formatQuantity renders a decimal quantity for a human. The wire value stays
// exact — this is display only, so a float here cannot corrupt anything.
func formatQuantity(q string) string {
	f, err := strconv.ParseFloat(q, 64)
	if err != nil {
		return q
	}
	return strconv.FormatFloat(f, 'f', 2, 64)
}

// parseTimeRef accepts an absolute RFC3339 timestamp or a relative offset into
// the past ("30d", "12h") — the two ways people ask about a period, without two
// pairs of flags that have to override each other.
func parseTimeRef(raw string) (time.Time, error) {
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, nil
	}
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		n, err := strconv.Atoi(days)
		if err == nil {
			return time.Now().AddDate(0, 0, -n), nil
		}
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return time.Now().Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("%q is neither an RFC3339 timestamp nor an offset like 30d or 12h", raw)
}

func init() {
	usageCmd.Flags().String("from", "", "Start of the period: RFC3339 timestamp, or an offset like 30d/12h (default 30d)")
	usageCmd.Flags().String("to", "", "End of the period: RFC3339 timestamp, or an offset like 1d (default now)")
	usageCmd.Flags().String("group-by", "", "Break down by app, day, project, or resource; combine axes with a comma (day,app)")
	usageCmd.Flags().String("app", "", "Limit to one app or database (name or id)")
	usageCmd.Flags().Bool("org-wide", false, "Roll up every project in the organization")
	rootCmd.AddCommand(usageCmd)
}
