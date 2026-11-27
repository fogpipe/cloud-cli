package cli

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show what the project or organization holds against what it may hold",
	Long: `Show, for each dimension of what the organization holds, what it may
hold, what its bound counts, what is reserved and what is actually used.

Each dimension in its own unit, never added together: CPU in cores, memory and
storage in bytes, instances and objects as counts, HTTP requests per hour.

With --org-wide, ALLOWED is the organization's bound and COUNTED what that
bound counts now, the number a create is refused by, with what it counts:
"booked" on CPU, memory, storage and instances, which is what may run at once
(every app at its max scale, every runner at its max runners, every database
at its instances); "stored" on object storage, the registry, backups and
objects, as last measured; "written" on logs. A project shows BOOKED, its own
part of what the bound counts, and says when its organization is at or near a
bound; an owner asks for more with ` + "`fpcloud org quota request`" + `.

RESERVED is the limits and volume sizes of what ran, and USED what was
measurably used, each in the latest hour the platform has recorded, which HOUR
names (UTC): an hour is recorded about 50 minutes after it ends, so neither is
the present. BOOKED and COUNTED are read now.

"no reading" is a figure the platform could not read, or has not recorded
yet: a project created after the latest recorded hour has none. 0 is a
measured zero,
and "-" is a figure the dimension does not have: backups reserve nothing
ahead, a count has no use over time, and HTTP requests have no bound yet.
NOT MEASURED names what a dimension reserves whose use is not read. A
database's storage use is its data size. HTTP requests are what the edge
served the scope's apps and websites, an hour's worth averaged over the hours
the platform read.

  fpcloud usage                     this project
  fpcloud usage --org-wide          the whole organization
  fpcloud usage --rows              and what each resource books now and held over 30 days
  fpcloud usage --org-wide --rows --from 7d
  fpcloud usage -o json             every figure, with each dimension's series`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var q client.CapacityQuery
		for _, f := range []struct {
			flag string
			into *string
		}{{"from", &q.From}, {"to", &q.To}} {
			raw, _ := cmd.Flags().GetString(f.flag)
			if raw == "" {
				continue
			}
			ts, err := parseTimeRef(raw)
			if err != nil {
				return fmt.Errorf("--%s: %w", f.flag, err)
			}
			*f.into = ts.UTC().Format(time.RFC3339)
		}
		if step, _ := cmd.Flags().GetString("step"); step != "" {
			if step != "hour" && step != "day" {
				return fmt.Errorf("--step is hour or day")
			}
			q.Step = step
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
				report, err = c.OrgCapacity(cmd.Context(), orgID, q)
			})
		} else {
			project, perr := requireProject()
			if perr != nil {
				return perr
			}
			withSpinner("Reading usage...", func() {
				report, err = c.ProjectCapacity(cmd.Context(), project, q)
			})
		}
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(report)
		}
		renderTable(capacityRows(report))
		if near := capacityNear(report); near != "" {
			fmt.Println()
			fmt.Println(near)
		}
		if withRows {
			for _, a := range report.Axes {
				if len(a.Rows) == 0 {
					continue
				}
				period := fmt.Sprintf("averaged over %s to %s", report.From.UTC().Format("2006-01-02 15:04"), report.To.UTC().Format("2006-01-02 15:04 MST"))
				switch {
				case a.Counts == "booked" && !a.Reserves && !a.MeasuresUse:
					period = "booked now"
				case a.Counts == "booked":
					period = "booked now; reserved and used " + period
				}
				fmt.Printf("\n%s, %s\n", capacityAxisLabel(a.Axis), period)
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
	"registry_storage": "Registry", "backup_storage": "Backups", "log_bytes_per_second": "Logs", "http_requests": "HTTP requests",
	"pods": "Instances", "objects": "Objects",
}

// capacityUnmeasuredWords are what NOT MEASURED calls each kind; a kind not
// named prints as the API spells it.
var capacityUnmeasuredWords = map[string]string{
	"job": "jobs", "runner": "runners", "volume": "volumes",
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
	case "byte_per_second":
		return humanizeSize(int64(math.Round(*v))) + "/s"
	case "count":
		return strconv.FormatInt(int64(math.Round(*v)), 10)
	case "request_per_hour":
		return strconv.FormatFloat(*v, 'f', requestRateDigits(*v), 64) + "/h"
	}
	return strconv.FormatFloat(*v, 'g', 4, 64)
}

// capacityRows is one row per dimension: at organization scope the bound and
// what it counts, at project scope what the project books, then what the scope
// reserved and used in the latest hour recorded and which hour that was, "-"
// where the dimension has no such figure at all. A project read carries no
// bound, so its table has no column for one.
func capacityRows(r *client.CapacityReport) ([]string, [][]string) {
	orgScope := r.Scope == "organization"
	headers := []string{"DIMENSION", "BOOKED", "RESERVED", "USED", "HOUR", "NOT MEASURED"}
	if orgScope {
		headers = []string{"DIMENSION", "ALLOWED", "COUNTED", "RESERVED", "USED", "HOUR", "NOT MEASURED"}
	}
	rows := make([][]string, 0, len(r.Axes))
	for _, a := range r.Axes {
		allowed, counted, reserved, used := "-", "-", "-", "-"
		if a.Bounded {
			allowed, counted = capacityFigure(a.Allowed, a.Unit), capacityFigure(a.Counted, a.Unit)
			if a.Counted != nil && a.Counts != "" {
				counted += " " + a.Counts
			}
		} else if !orgScope && a.Counts == "booked" {
			counted = capacityFigure(a.Counted, a.Unit)
		}
		if a.Reserves {
			reserved = capacityFigure(a.Reserved, a.Unit)
		}
		if a.MeasuresUse {
			used = capacityFigure(a.Used, a.Unit)
		}
		unmeasured := make([]string, len(a.Unmeasured))
		for i, k := range a.Unmeasured {
			unmeasured[i] = k
			if w, ok := capacityUnmeasuredWords[k]; ok {
				unmeasured[i] = w
			}
		}
		row := []string{capacityAxisLabel(a.Axis), counted, reserved, used, capacityHours(a), strings.Join(unmeasured, ", ")}
		if orgScope {
			row = append([]string{row[0], allowed}, row[1:]...)
		}
		rows = append(rows, row)
	}
	return headers, rows
}

// capacityHours names the hour an axis's reserved and used figures are from,
// once when both share it: the latest each source recorded, never the present.
func capacityHours(a client.CapacityAxis) string {
	var hours []string
	for _, h := range []struct {
		has bool
		at  *time.Time
	}{{a.Reserves, a.ReservedHour}, {a.MeasuresUse, a.UsedHour}} {
		if !h.has || h.at == nil {
			continue
		}
		label := h.at.UTC().Format("Jan 2 15:04")
		if len(hours) == 0 || hours[0] != label {
			hours = append(hours, label)
		}
	}
	if len(hours) == 0 {
		return "-"
	}
	return strings.Join(hours, " / ")
}

// capacityNear says which dimensions a project's organization is at or near
// its bound on, and who can ask for more; empty when none is, and on an
// organization's read, whose table shows the bound itself. At the bound a
// create is refused, which is the reader's first question, so it comes first.
func capacityNear(r *client.CapacityReport) string {
	if r.Scope == "organization" {
		return ""
	}
	var at, near []string
	for _, a := range r.Axes {
		switch a.OrgBound {
		case "at":
			at = append(at, capacityAxisLabel(a.Axis))
		case "near":
			near = append(near, capacityAxisLabel(a.Axis))
		}
	}
	var lines []string
	if len(at) > 0 {
		lines = append(lines, fmt.Sprintf("The organization is at its bound on %s: creates on it are refused.", strings.Join(at, ", ")))
	}
	if len(near) > 0 {
		lines = append(lines, fmt.Sprintf("The organization is near its bound on %s.", strings.Join(near, ", ")))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\nAn owner can ask for more with `fpcloud org quota request`."
}

// capacityShareRows is what each project or resource of the scope books now,
// where the dimension's bound counts what is booked, largest first, and what it
// held on the dimension averaged over the period; a deleted one is tagged,
// since a live one may carry its name.
func capacityShareRows(a client.CapacityAxis) ([]string, [][]string) {
	booked := a.Counts == "booked"
	headers := []string{"KIND", "NAME", "RESERVED", "USED"}
	held := a.Rows
	if booked {
		headers = []string{"KIND", "NAME", "BOOKED", "RESERVED", "USED"}
		held = append([]client.CapacityRow(nil), a.Rows...)
		sort.SliceStable(held, func(i, j int) bool { return figureOrLess(held[i].Counted) > figureOrLess(held[j].Counted) })
	}
	rows := make([][]string, 0, len(held))
	for _, r := range held {
		name := r.Name
		if name == "" {
			name = r.ID
		}
		switch {
		case r.Kind == "project" && r.ID == "":
			// Measured on the project and no resource of it.
			name = "(project)"
		case r.Kind == "volume":
			name = "(volumes no database owns)"
		}
		reserved, used := "-", "-"
		if a.Reserves {
			reserved = capacityFigure(r.Reserved, a.Unit)
		}
		if a.MeasuresUse {
			used = capacityFigure(r.Used, a.Unit)
		}
		row := []string{r.Kind, deletedName(name, r.Deleted), reserved, used}
		if booked {
			row = []string{r.Kind, deletedName(name, r.Deleted), capacityFigure(r.Counted, a.Unit), reserved, used}
		}
		rows = append(rows, row)
	}
	if booked && !a.Reserves && !a.MeasuresUse {
		// A count holds nothing over time: what each holder books is all.
		for i := range rows {
			rows[i] = rows[i][:3]
		}
		headers = headers[:3]
	}
	return headers, rows
}

// figureOrLess orders an unread figure below every read one.
func figureOrLess(v *float64) float64 {
	if v == nil {
		return math.Inf(-1)
	}
	return *v
}

// requestRateDigits keeps a rate under ten requests an hour readable (0.4/h)
// and every larger one whole.
func requestRateDigits(v float64) int {
	if v < 10 {
		return 1
	}
	return 0
}

// formatQuantity renders a decimal quantity for a human, to two places. It
// rounds the wire's decimal exactly, halves away from zero, as the console does:
// through a float, a quantity like 235.5349999999999998608 rounded one way here
// and the other way there, because each surface rounded a different nearest
// double. The wire value stays exact; this is display only.
func formatQuantity(q string) string {
	r, ok := new(big.Rat).SetString(q)
	if !ok {
		return q
	}
	return r.FloatString(2)
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
