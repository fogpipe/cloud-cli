package cli

import (
	"fmt"
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
		wanted, _ := cmd.Flags().GetString("to")
		if wanted == "" {
			return fmt.Errorf("--to is required: the bound to ask for")
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
			{"Bound", req.Bound},
			{"Asked for", req.Wanted},
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
			rows = append(rows, []string{capacityAxisLabel(r.Axis), r.Bound, r.Wanted, r.RequestedBy,
				r.CreatedAt.UTC().Format("2006-01-02 15:04"), r.Note})
		}
		render(headers, rows, reqs)
		return nil
	},
}

func init() {
	orgQuotaRequestCmd.Flags().String("to", "", "The bound to ask for, in the dimension's own spelling (16, 32Gi, 40)")
	orgQuotaRequestCmd.Flags().String("note", "", "Why, for the operators reading the request")
	orgQuotaCmd.AddCommand(orgQuotaRequestCmd)
	orgQuotaCmd.AddCommand(orgQuotaRequestsCmd)
	orgCmd.AddCommand(orgQuotaCmd)
}
