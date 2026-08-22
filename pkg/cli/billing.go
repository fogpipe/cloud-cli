package cli

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

var billingCmd = &cobra.Command{
	Use:   "billing",
	Short: "See what your organization is spending",
	Long: `Cost, invoices, and who is allowed to see them.

Billing access is a SEPARATE permission from the one that lets you read a
project. Being an organization owner does not grant it — someone with
billing.admin has to give you billing.viewer or billing.admin first. Metered
quantities (` + "`fpcloud usage`" + `) stay readable to anyone who can read the project.`,
}

var billingCostCmd = &cobra.Command{
	Use:   "cost",
	Short: "Show the cost of the period in progress",
	Long: `Estimate what the current period will cost.

This is computed on demand, not an invoice: the usage is still accruing and the
number changes every hour. The default period is the current calendar month in
UTC — the same period that becomes the invoice, so this and the eventual bill
cover the same hours.

  fpcloud billing cost
  fpcloud billing cost --from 2026-07-01T00:00:00Z --to 2026-08-01T00:00:00Z`,
	RunE: func(cmd *cobra.Command, args []string) error {
		q := url.Values{}
		for _, f := range []string{"from", "to"} {
			raw, _ := cmd.Flags().GetString(f)
			if raw == "" {
				continue
			}
			ts, err := parseTimeRef(raw)
			if err != nil {
				return err
			}
			q.Set(f, ts.UTC().Format(time.RFC3339))
		}
		// Both or neither: a half-specified period would silently answer a
		// different question than the one asked.
		if len(q) == 1 {
			return fmt.Errorf("give both --from and --to, or neither")
		}

		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		c := getClient()
		var rated *client.RatedPeriod
		withSpinner("Calculating cost...", func() {
			rated, err = c.GetBillingEstimate(cmd.Context(), orgID, q.Encode())
		})
		if err != nil {
			return err
		}

		rows := make([][]string, 0, len(rated.Lines))
		for _, l := range rated.Lines {
			amount := l.Amount
			price := l.UnitPrice
			if !l.Priced {
				// Never blank, and never zero: a resource with no price is not
				// free, it is unpriced, and a blank cell reads as "costs
				// nothing".
				amount, price = "not priced", "—"
			}
			// The quantity is display-rounded here and the amount is not: the
			// amount arrives already rounded to the cent it will be billed at, so
			// touching it would put a second opinion about money in the CLI.
			rows = append(rows, []string{l.ResourceType, formatQuantity(l.Quantity), l.Unit, price, amount})
		}
		render([]string{"RESOURCE", "QUANTITY", "UNIT", "UNIT PRICE", "AMOUNT"}, rows, rated)

		fmt.Printf("\n  Total: %s %s\n", rated.Total, rated.Currency)
		if len(rated.UnpricedTypes) > 0 {
			// The total excludes these, so saying nothing would present an
			// understatement as complete.
			fmt.Printf("  Excludes unpriced: %s\n", strings.Join(rated.UnpricedTypes, ", "))
		}
		fmt.Println(mutedStyle.Render("  Estimate for the period so far — not an invoice."))
		return nil
	},
}

var billingPricesCmd = &cobra.Command{
	Use:   "prices",
	Short: "Show what each resource costs",
	Long: `The platform's current price list.

Rates are per unit of whatever the resource is metered in — a core-hour of CPU,
a GiB-hour of memory or storage. Multiply by what ` + "`fpcloud usage`" + ` reports to
get what ` + "`fpcloud billing cost`" + ` will say.

This needs no account and no billing role: a price is a published fact.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		var prices []*client.Price
		var err error
		withSpinner("Fetching prices...", func() {
			prices, err = c.ListPrices(cmd.Context())
		})
		if err != nil {
			return err
		}
		rows := make([][]string, len(prices))
		for i, p := range prices {
			rows[i] = []string{p.ResourceType, p.UnitPrice + " " + p.Currency}
		}
		render([]string{"RESOURCE", "UNIT PRICE"}, rows, prices)
		return nil
	},
}

var billingInvoicesCmd = &cobra.Command{
	Use:   "invoices [invoice-id]",
	Short: "List invoices for closed periods, or show one of them",
	Long: `An invoice per closed period, or one invoice in full.

Named without an argument this lists the periods, which carry a total and
nothing else. Name an invoice and it is shown with the line items the total is
made of — that is the only place they are readable, and the only way to tell a
period rated to zero from one with no lines rated yet.

  fpcloud billing invoices
  fpcloud billing invoices 2a11900f-4773-47ce-8b6e-7182e628fdbf`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		c := getClient()
		if len(args) == 1 {
			return showInvoice(cmd, c, orgID, args[0])
		}

		var invoices []*client.Invoice
		withSpinner("Fetching invoices...", func() {
			invoices, err = c.ListInvoices(cmd.Context(), orgID)
		})
		if err != nil {
			return err
		}
		rows := make([][]string, len(invoices))
		for i, inv := range invoices {
			rows[i] = []string{
				inv.PeriodStart.UTC().Format("2006-01"),
				inv.Status,
				inv.Total + " " + inv.Currency,
				inv.ID,
			}
		}
		render([]string{"PERIOD", "STATUS", "TOTAL", "ID"}, rows, invoices)
		return nil
	},
}

func showInvoice(cmd *cobra.Command, c *client.Client, orgID, invoiceID string) error {
	var inv *client.Invoice
	var err error
	withSpinner("Fetching invoice...", func() {
		inv, err = c.GetInvoice(cmd.Context(), orgID, invoiceID)
	})
	if err != nil {
		return err
	}

	rows := make([][]string, len(inv.Lines))
	for i, l := range inv.Lines {
		project := l.ProjectName
		if project == "" {
			project = "(org)"
		}
		rows[i] = []string{project, l.ResourceType, formatQuantity(l.Quantity), l.Unit, l.UnitPrice, l.Amount}
	}
	// The unit price is a column, not a footnote: it is the rate this line
	// was BILLED at, stored on the invoice, and it is what a dispute is
	// settled against. A period spanning a price change shows the same
	// resource twice, once per rate.
	render([]string{"PROJECT", "RESOURCE", "QUANTITY", "UNIT", "UNIT PRICE", "AMOUNT"}, rows, inv)

	fmt.Printf("\n  %s — %s\n", inv.PeriodStart.UTC().Format("2006-01-02"), inv.PeriodEnd.UTC().Format("2006-01-02"))
	fmt.Printf("  Total: %s %s (%s)\n", inv.Total, inv.Currency, inv.Status)
	return nil
}

var billingRolesCmd = &cobra.Command{
	Use:   "roles",
	Short: "Manage who may see billing",
}

var billingRolesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List billing role grants",
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		c := getClient()
		var bindings []*client.BillingBinding
		withSpinner("Fetching billing roles...", func() {
			bindings, err = c.ListBillingBindings(cmd.Context(), orgID)
		})
		if err != nil {
			return err
		}
		rows := make([][]string, len(bindings))
		for i, b := range bindings {
			rows[i] = []string{b.Member, b.MemberType, b.Role}
		}
		render([]string{"MEMBER", "TYPE", "ROLE"}, rows, bindings)
		return nil
	},
}

var billingRolesGrantCmd = &cobra.Command{
	Use:   "grant <member>",
	Short: "Grant a billing role (billing.viewer or billing.admin)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		role, _ := cmd.Flags().GetString("role")
		if role != client.BillingViewer && role != client.BillingAdmin {
			return fmt.Errorf("--role must be %s or %s", client.BillingViewer, client.BillingAdmin)
		}
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		memberType, _ := cmd.Flags().GetString("member-type")
		c := getClient()
		var b *client.BillingBinding
		withSpinner("Granting billing role...", func() {
			b, err = c.GrantBillingBinding(cmd.Context(), orgID, args[0], memberType, role)
		})
		if err != nil {
			return err
		}
		fmt.Printf("Granted %s to %s\n", b.Role, b.Member)
		return nil
	},
}

var billingRolesRevokeCmd = &cobra.Command{
	Use:   "revoke <member>",
	Short: "Revoke a member's billing role",
	Long: `Remove a member's billing role.

An organization's last billing.admin cannot be revoked: without one, nobody can
see the bill or grant the role back. Grant someone else billing.admin first.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		memberType, _ := cmd.Flags().GetString("member-type")
		c := getClient()
		withSpinner("Revoking billing role...", func() {
			err = c.RevokeBillingBinding(cmd.Context(), orgID, args[0], memberType)
		})
		if err != nil {
			return err
		}
		fmt.Printf("Revoked billing access for %s\n", args[0])
		return nil
	},
}

var billingBudgetCmd = &cobra.Command{
	Use:   "budget",
	Short: "Set a spend target and get told when you approach it",
	Long: `A budget is an alerting threshold, never a cap.

Nothing is refused when you cross it — fpcloud records the crossing and says so.
A cap would have to answer "what happens when it is hit", and every answer
(refusing deploys, stopping workloads) is worse than the overspend it prevents.

Reading a budget needs a billing role; setting one needs billing.admin.`,
}

var billingBudgetShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the budget and the thresholds already crossed",
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		c := getClient()
		var view *client.BudgetView
		withSpinner("Fetching budget...", func() {
			view, err = c.GetBudget(cmd.Context(), orgID)
		})
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			render(nil, nil, view)
			return nil
		}
		if view.Budget == nil {
			fmt.Println("No budget set. `fpcloud billing budget set --amount 100` to add one.")
			return nil
		}
		fmt.Printf("Budget: %s %s  (alerts at %s)\n",
			view.Budget.Amount, view.Budget.Currency, percentList(view.Budget.Thresholds))
		if len(view.Alerts) == 0 {
			fmt.Println("No thresholds crossed.")
			return nil
		}
		rows := make([][]string, len(view.Alerts))
		for i, a := range view.Alerts {
			rows[i] = []string{
				a.PeriodStart.Format("2006-01"),
				fmt.Sprintf("%d%%", a.ThresholdPercent),
				a.Amount + " " + a.Currency,
				a.CreatedAt.Format(time.RFC3339),
			}
		}
		render([]string{"PERIOD", "THRESHOLD", "SPEND AT CROSSING", "WHEN"}, rows, view.Alerts)
		return nil
	},
}

var billingBudgetSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set the budget for the period",
	RunE: func(cmd *cobra.Command, args []string) error {
		amount, _ := cmd.Flags().GetString("amount")
		if amount == "" {
			return fmt.Errorf("--amount is required")
		}
		thresholds, _ := cmd.Flags().GetIntSlice("alert-at")
		currency, _ := cmd.Flags().GetString("currency")
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		c := getClient()
		var b *client.BillingBudget
		withSpinner("Setting budget...", func() {
			b, err = c.SetBudget(cmd.Context(), orgID, client.SetBudgetRequest{
				Amount: amount, Currency: currency, Thresholds: thresholds,
			})
		})
		if err != nil {
			return err
		}
		fmt.Printf("Budget set to %s %s; alerts at %s\n", b.Amount, b.Currency, percentList(b.Thresholds))
		return nil
	},
}

var billingBudgetUnsetCmd = &cobra.Command{
	Use:   "unset",
	Short: "Remove the budget, stopping further alerts",
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		c := getClient()
		withSpinner("Removing budget...", func() {
			err = c.DeleteBudget(cmd.Context(), orgID)
		})
		if err != nil {
			return err
		}
		fmt.Println("Budget removed. Crossings already recorded are kept.")
		return nil
	},
}

// percentList renders thresholds as "50%, 90%, 100%".
func percentList(thresholds []int) string {
	parts := make([]string, len(thresholds))
	for i, t := range thresholds {
		parts[i] = fmt.Sprintf("%d%%", t)
	}
	return strings.Join(parts, ", ")
}

func init() {
	billingCostCmd.Flags().String("from", "", "Start of the period: RFC3339 timestamp, or an offset like 30d/12h")
	billingCostCmd.Flags().String("to", "", "End of the period: RFC3339 timestamp, or an offset like 1d")

	billingRolesGrantCmd.Flags().String("role", "", "billing.viewer or billing.admin (required)")
	billingRolesGrantCmd.Flags().String("member-type", "", "user (default) or serviceAccount")
	billingRolesRevokeCmd.Flags().String("member-type", "", "user (default) or serviceAccount")

	billingRolesCmd.AddCommand(billingRolesListCmd, billingRolesGrantCmd, billingRolesRevokeCmd)

	billingBudgetSetCmd.Flags().String("amount", "", "Target spend for the period, e.g. 250 (required)")
	billingBudgetSetCmd.Flags().String("currency", "", "Currency (default EUR)")
	billingBudgetSetCmd.Flags().IntSlice("alert-at", nil, "Percentages to alert at (default 50,90,100)")
	billingBudgetCmd.AddCommand(billingBudgetShowCmd, billingBudgetSetCmd, billingBudgetUnsetCmd)

	billingCmd.AddCommand(billingCostCmd, billingPricesCmd, billingInvoicesCmd, billingRolesCmd, billingBudgetCmd)
	rootCmd.AddCommand(billingCmd)
}
