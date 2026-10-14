package cli

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show what the project or organization holds against what it may hold",
	Long: `Show, for each dimension the organization is bounded on, what it may
hold, what is reserved and what is actually used.

Each dimension in its own unit, never added together: CPU in cores, memory and
storage in bytes, instances and objects as counts. ALLOWED is the
organization's bound and ADMITTED what that bound counts now, the number a
create is refused by. RESERVED is what the scope holds booked (limits and
quotas) and USED what it measurably uses, both now. A project shows its own
reserved and used beside its organization's bound.

"no reading" is a figure the platform could not read, 0 is a measured zero,
and "-" is a figure the dimension does not have: backups reserve nothing ahead,
and a count has no use over time. NOT MEASURED names what a dimension reserves
whose use is not read. A database's storage use is its data size.

  fpcloud usage                     this project
  fpcloud usage --org-wide          the whole organization
  fpcloud usage --rows              and each resource's share, over 30 days
  fpcloud usage --org-wide --rows --from 7d
  fpcloud usage -o json             every figure, with each dimension's series`,
	Args: cobra.NoArgs,
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
			q.Set(f, ts.UTC().Format(time.RFC3339))
		}
		if step, _ := cmd.Flags().GetString("step"); step != "" {
			if step != "hour" && step != "day" {
				return fmt.Errorf("--step is hour or day")
			}
			q.Set("step", step)
		}
		orgWide, _ := cmd.Flags().GetBool("org-wide")
		withRows, _ := cmd.Flags().GetBool("rows")

		c := getClient()
		var report *client.CapacityReport
		var err error
		if orgWide {
			orgID, oerr := resolveOrgID(cmd)
			if oerr != nil {
				return oerr
			}
			withSpinner("Reading usage...", func() {
				report, err = c.OrgCapacity(cmd.Context(), orgID, q.Encode())
			})
		} else {
			project, perr := requireProject()
			if perr != nil {
				return perr
			}
			withSpinner("Reading usage...", func() {
				report, err = c.ProjectCapacity(cmd.Context(), project, q.Encode())
			})
		}
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(report)
		}
		renderTable(capacityRows(report))
		if withRows {
			for _, a := range report.Axes {
				if len(a.Rows) == 0 {
					continue
				}
				fmt.Printf("\n%s, averaged over %s to %s\n", capacityAxisLabel(a.Axis),
					report.From.UTC().Format("2006-01-02 15:04"), report.To.UTC().Format("2006-01-02 15:04 MST"))
				renderTable(capacityShareRows(a))
			}
		}
		return nil
	},
}

// capacityAxisLabels are the words for the axes the capacity read reports; an
// axis not named here prints as the API spells it.
var capacityAxisLabels = map[string]string{
	"cpu": "CPU", "memory": "Memory", "storage": "Storage", "object_storage": "Object storage",
	"registry_storage": "Registry", "backup_storage": "Backups", "pods": "Instances", "objects": "Objects",
}

func capacityAxisLabel(axis string) string {
	if l, ok := capacityAxisLabels[axis]; ok {
		return l
	}
	return axis
}

// capacityFigure is one figure in its axis's unit. A nil figure is one the
// platform could not read, which is never a zero.
func capacityFigure(v *float64, unit string) string {
	if v == nil {
		return "no reading"
	}
	switch unit {
	case "core":
		return formatMillicores(int64(math.Round(*v * 1000)))
	case "byte":
		return humanizeSize(int64(math.Round(*v)))
	case "count":
		return strconv.FormatInt(int64(math.Round(*v)), 10)
	}
	return strconv.FormatFloat(*v, 'g', 4, 64)
}

// capacityRows is one row per dimension, now: the bound and what it counts,
// then what the scope reserves and uses, "-" where the dimension has no such
// figure at all.
func capacityRows(r *client.CapacityReport) ([]string, [][]string) {
	headers := []string{"DIMENSION", "ALLOWED", "ADMITTED", "RESERVED", "USED", "NOT MEASURED"}
	rows := make([][]string, 0, len(r.Axes))
	for _, a := range r.Axes {
		reserved, used := "-", "-"
		if a.Reserves {
			reserved = capacityFigure(a.Reserved, a.Unit)
		}
		if a.MeasuresUse {
			used = capacityFigure(a.Used, a.Unit)
		}
		unmeasured := make([]string, len(a.Unmeasured))
		for i, k := range a.Unmeasured {
			unmeasured[i] = k + "s"
		}
		rows = append(rows, []string{capacityAxisLabel(a.Axis), capacityFigure(a.Allowed, a.Unit), capacityFigure(a.Admitted, a.Unit),
			reserved, used, strings.Join(unmeasured, ", ")})
	}
	return headers, rows
}

// capacityShareRows is what each project or resource of the scope held on one
// dimension, averaged over the period; a deleted one is tagged, since a live
// one may carry its name.
func capacityShareRows(a client.CapacityAxis) ([]string, [][]string) {
	headers := []string{"KIND", "NAME", "RESERVED", "USED"}
	rows := make([][]string, 0, len(a.Rows))
	for _, r := range a.Rows {
		name := r.Name
		if name == "" {
			name = r.ID
		}
		if r.Kind == "project" && r.ID == "" {
			// Measured on the project and no resource of it.
			name = "(project)"
		}
		reserved, used := "-", "-"
		if a.Reserves {
			reserved = capacityFigure(r.Reserved, a.Unit)
		}
		if a.MeasuresUse {
			used = capacityFigure(r.Used, a.Unit)
		}
		rows = append(rows, []string{r.Kind, deletedName(name, r.Deleted), reserved, used})
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
	usageCmd.Flags().String("from", "", "Start of the period --rows averages over: RFC3339 timestamp, or an offset like 30d/12h (default 30d)")
	usageCmd.Flags().String("to", "", "End of the period: RFC3339 timestamp, or an offset like 1d (default now)")
	usageCmd.Flags().String("step", "", "The series' step in -o json: hour or day (default day; hour up to 31 days)")
	usageCmd.Flags().Bool("rows", false, "Also show what each project (with --org-wide) or resource held on each dimension over the period")
	usageCmd.Flags().Bool("org-wide", false, "The whole organization rather than the current project")
	rootCmd.AddCommand(usageCmd)
}
