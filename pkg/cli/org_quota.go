package cli

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var orgQuotaCmd = &cobra.Command{
	Use:   "quota",
	Short: "Ask for more of what the organization may hold",
	Long: `An organization's projects share one bound per dimension, set by the
platform. ` + "`fpcloud usage --org-wide`" + ` shows each bound and what is
counted against it; a create that would cross one is refused with both numbers.

An owner of the organization asks for a higher bound with
` + "`fpcloud org quota request`" + `. The request reaches the operators by mail,
and they answer the owner who asked.`,
}

// quotaDimensions are the bounds a request may name, in the order the help
// lists them.
var quotaDimensions = []string{"cpu", "memory", "pods", "storage", "object_storage", "objects", "registry_storage", "backup_storage"}

var orgQuotaRequestCmd = &cobra.Command{
	Use:   "request <dimension>",
	Short: "Ask the operators to raise one of the organization's bounds",
	Long: `Ask the operators to raise one of the organization's bounds to --to.

<dimension> is one of: ` + strings.Join(quotaDimensions, ", ") + `.
--to is the bound asked for in that dimension's own spelling: cores for cpu
("16"), bytes with a unit for memory and storage ("32Gi"), a number for pods
and objects. It must be above the current bound.

Only an owner of the organization may ask. An organization holds one open
request per dimension: asking again while one is open shows that one and sends
nothing new. A request closes when the bound is changed, or after seven days.

  fpcloud org quota request memory --to 32Gi --note "a second database"
  fpcloud org quota request cpu --to 16`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, _ := cmd.Flags().GetString("to")
		if raw == "" {
			return fmt.Errorf("--to is required: the bound to ask for")
		}
		wanted, err := parseBound(args[0], raw)
		if err != nil {
			return fmt.Errorf("--to: %w", err)
		}
		note, _ := cmd.Flags().GetString("note")
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		c := getClient()
		var req *client.QuotaRequest
		withSpinner("Sending request...", func() {
			req, err = c.RequestQuota(cmd.Context(), orgID, client.RequestQuotaRequest{Axis: args[0], Wanted: wanted, Note: note})
		})
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(req)
		}
		fmt.Println(renderInfoBox("Quota Request", [][]string{
			{"Dimension", capacityAxisLabel(req.Axis)},
			{"Bound", capacityFigure(req.Bound, quotaUnit(req.Axis))},
			{"Asked for", capacityFigure(&req.Wanted, quotaUnit(req.Axis))},
			{"Asked by", req.RequestedBy},
			{"Asked at", req.CreatedAt.UTC().Format("2006-01-02 15:04 MST")},
		}))
		fmt.Println(mutedStyle.Render("The operators answer by mail to the address above."))
		return nil
	},
}

var orgQuotaRequestsCmd = &cobra.Command{
	Use:   "requests",
	Short: "List the organization's open requests for a higher bound",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		c := getClient()
		var reqs []client.QuotaRequest
		withSpinner("Fetching requests...", func() {
			reqs, err = c.ListQuotaRequests(cmd.Context(), orgID)
		})
		if err != nil {
			return err
		}
		headers := []string{"DIMENSION", "BOUND", "ASKED FOR", "ASKED BY", "ASKED AT", "NOTE"}
		rows := make([][]string, 0, len(reqs))
		for _, r := range reqs {
			wanted := r.Wanted
			rows = append(rows, []string{capacityAxisLabel(r.Axis), capacityFigure(r.Bound, quotaUnit(r.Axis)), capacityFigure(&wanted, quotaUnit(r.Axis)), r.RequestedBy,
				r.CreatedAt.UTC().Format("2006-01-02 15:04"), r.Note})
		}
		render(headers, rows, reqs)
		return nil
	},
}

// quotaUnit is the unit a dimension's bound is counted in, as the capacity
// read reports it.
func quotaUnit(axis string) string {
	switch axis {
	case "cpu":
		return "core"
	case "pods", "objects":
		return "count"
	}
	return "byte"
}

// byteSuffixes are the units a byte bound may be written in, the binary ones
// first so "Gi" is not read as "G" and a stray "i".
var byteSuffixes = []struct {
	suffix string
	scale  float64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40},
	{"K", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12},
}

// parseBound reads --to in the dimension's own spelling into its base unit:
// cores for cpu ("16", "1500m"), bytes for storage ("32Gi", "500G"), a whole
// number for a count.
func parseBound(axis, raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	switch quotaUnit(axis) {
	case "count":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%s is a whole number above zero, not %q", axis, raw)
		}
		return float64(n), nil
	case "core":
		scale := 1.0
		if v, ok := strings.CutSuffix(raw, "m"); ok {
			raw, scale = v, 0.001
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v <= 0 || math.IsInf(v, 0) {
			return 0, fmt.Errorf("cpu is cores, such as 16 or 1500m, not %q", raw)
		}
		return v * scale, nil
	}
	scale := 1.0
	for _, s := range byteSuffixes {
		if v, ok := strings.CutSuffix(raw, s.suffix); ok {
			raw, scale = v, s.scale
			break
		}
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s is a size such as 32Gi, not %q", axis, raw)
	}
	return math.Round(v * scale), nil
}

func init() {
	orgQuotaRequestCmd.Flags().String("to", "", "The bound to ask for, in the dimension's own spelling (16, 32Gi, 40)")
	orgQuotaRequestCmd.Flags().String("note", "", "Why, for the operators reading the request")
	orgQuotaCmd.AddCommand(orgQuotaRequestCmd)
	orgQuotaCmd.AddCommand(orgQuotaRequestsCmd)
	orgCmd.AddCommand(orgQuotaCmd)
}
