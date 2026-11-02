package cli

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
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
billing.admin has to give you billing.viewer or billing.admin first. A
project's own cost (` + "`fpcloud billing cost --project`" + `) and its metered
quantities (` + "`fpcloud usage`" + `) stay readable to anyone who can read the
project.`,
}

var billingCostCmd = &cobra.Command{
	Use:   "cost",
	Short: "Show what your organization or a project cost",
	Long: `Show what the organization, or one project, cost over a period.

Computed from metered usage at your rates, not an invoice: the default period is
the month in progress, so the number grows every hour. Amounts are shown to the
cent; the invoice rounds once per line and is the bill.

--group-by takes up to one of project, resource and type (a resource is an app,
database, job, runner or bucket) and up to one of hour, day and month. Grouping
by type shows quantities and rates, one row per rate that applied.

--resource narrows the read to one app, database, job, runner or bucket by its
name or id, found among the resources that cost something in the period, so a
deleted one still answers. A name two resources share is refused with each
one's id. With the default grouping it is that resource by
type.

The organization's cost needs a billing role. A project's cost needs only read
on the project, and is priced at the organization's real rates.

  fpcloud billing cost
  fpcloud billing cost --group-by project,day
  fpcloud billing cost --project api --group-by resource
  fpcloud billing cost --project api --resource web
  fpcloud billing cost --from 2026-07-01T00:00:00Z --to 2026-08-01T00:00:00Z`,
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
				return err
			}
			q.Set(f, ts.UTC().Format(time.RFC3339))
		}
		// Both or neither: a half-specified period would silently answer a
		// different question than the one asked.
		if len(q) == 1 {
			return fmt.Errorf("give both --from and --to, or neither")
		}
		groupBy, err := costGroupBy(cmd)
		if err != nil {
			return err
		}
		q.Set("group_by", strings.Join(groupBy, ","))

		c := getClient()
		// The org by default, which is what this command has always shown. A
		// project only when one is named on the command line: the current
		// project from config is the default for commands about a project, and
		// would otherwise turn the org's cost into one project's unasked.
		var read func(url.Values) (*client.CostReport, error)
		if cmd.Flag("project").Changed {
			project, perr := requireProject()
			if perr != nil {
				return perr
			}
			read = func(v url.Values) (*client.CostReport, error) {
				return c.ProjectCost(cmd.Context(), project, v.Encode())
			}
		} else {
			orgID, oerr := resolveOrgID(cmd)
			if oerr != nil {
				return oerr
			}
			read = func(v url.Values) (*client.CostReport, error) { return c.OrgCost(cmd.Context(), orgID, v.Encode()) }
		}
		var report *client.CostReport
		withSpinner("Calculating cost...", func() {
			// A resource is named the way the record names it: read the period
			// by resource first, so one deleted since still resolves.
			if name, _ := cmd.Flags().GetString("resource"); name != "" {
				byResource := url.Values{}
				for k, v := range q {
					byResource[k] = v
				}
				byResource.Set("group_by", "resource")
				var all *client.CostReport
				if all, err = read(byResource); err != nil {
					return
				}
				var id string
				if id, err = costResourceID(all, name); err != nil {
					return
				}
				q.Set("resource", id)
			}
			report, err = read(q)
		})
		if err != nil {
			return err
		}

		headers, rows := costRows(report)
		render(headers, rows, report)

		fmt.Printf("\n  Total: %s %s\n", report.Total, report.Currency)
		// A credit is taken off the month's invoice, never off the usage
		// (ADR-276), so the total stays what the usage cost, and what the
		// credit took off and what is due are said beneath it.
		if len(report.Credits) > 0 {
			for _, line := range creditLines(report, time.Now()) {
				fmt.Println("  " + line)
			}
			fmt.Printf("  Amount due: %s %s\n", report.AmountDue, report.Currency)
		}
		if len(report.Unpriced) > 0 {
			// The total excludes these, so saying nothing would present an
			// understatement as complete.
			parts := make([]string, len(report.Unpriced))
			for i, u := range report.Unpriced {
				parts[i] = u.ResourceType + " " + formatQuantity(u.Quantity) + " " + u.Unit
			}
			fmt.Printf("  Not in the total, no rate yet: %s\n", strings.Join(parts, ", "))
		}
		// Which book produced these rates, next to the total they produced. An
		// org on a card other than the published one otherwise has no way to see
		// why its figures differ from the price page.
		if books := costBooks(report.PriceBooks); books != "" {
			fmt.Println(mutedStyle.Render("  " + books))
		}
		fmt.Println(mutedStyle.Render("  Computed from usage so far, not an invoice."))
		return nil
	},
}

// creditLines says how each month's credit stands: what of it the
// month's usage consumed, and for the month in progress what is left, how
// many days of the month remain, and when it runs out at the month's pace
// where the server could say. Over the allowance it is fully used, and the
// amount due beneath says what is invoiced above it. An unused part lapses
// with its month, so a past month says only what was applied.
func creditLines(report *client.CostReport, now time.Time) []string {
	now = now.UTC()
	lines := make([]string, 0, len(report.Credits))
	for _, cr := range report.Credits {
		line := fmt.Sprintf("Credit %s: %s of %s %s applied",
			cr.PeriodStart.UTC().Format("2006-01"), formatAmount(cr.Applied), formatAmount(cr.Allowance), report.Currency)
		end := cr.PeriodStart.UTC().AddDate(0, 1, 0)
		if now.Before(cr.PeriodStart.UTC()) || !now.Before(end) {
			lines = append(lines, line)
			continue
		}
		left := amountLeft(cr.Allowance, cr.Applied)
		days := daysLeft(now, end)
		switch {
		case left.Sign() <= 0:
			line += ", fully used"
		default:
			line += fmt.Sprintf(", %s left, %s", left.FloatString(2), days)
			if cr.RunsOutAt != nil {
				line += fmt.Sprintf("; at this month's pace it runs out around %s", cr.RunsOutAt.UTC().Format("2 Jan"))
			}
		}
		lines = append(lines, line)
	}
	return lines
}

// amountLeft is the allowance less what was applied, exact.
func amountLeft(allowance, applied string) *big.Rat {
	a, ok := new(big.Rat).SetString(allowance)
	if !ok {
		return new(big.Rat)
	}
	b, ok := new(big.Rat).SetString(applied)
	if !ok {
		return new(big.Rat)
	}
	return a.Sub(a, b)
}

// daysLeft counts the days of the month still to come, the day in progress
// included, in UTC: the billing month is a UTC month.
func daysLeft(now, end time.Time) string {
	days := int(math.Ceil(end.Sub(now).Hours() / 24))
	if days <= 1 {
		return "last day of the month (UTC)"
	}
	return fmt.Sprintf("%d days left in the month (UTC)", days)
}

// costResourceID finds the one resource a name means among the rows of a read
// grouped by resource. An id is taken as given. A name is unique only among one
// kind's live resources, so two of one project's can share it (an app and a
// bucket, or a deleted resource and the one that took its name), and the
// refusal lists each by its id, the one spelling that tells them apart, with
// what it cost; --project is suggested only where the projects differ.
func costResourceID(report *client.CostReport, name string) (string, error) {
	var found []client.CostRow
	seen := map[string]bool{}
	for _, r := range report.Rows {
		if r.ResourceID == "" || seen[r.ResourceID] || (r.ResourceID != name && r.ResourceName != name) {
			continue
		}
		seen[r.ResourceID] = true
		found = append(found, r)
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no resource named %q cost anything in this period", name)
	case 1:
		return found[0].ResourceID, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q names more than one resource; pass one's id to --resource", name)
	projects := map[string]bool{}
	for _, r := range found {
		projects[r.ProjectName] = true
		fmt.Fprintf(&b, "\n  %s  %s in %s, %s %s", r.ResourceID, deletedName(r.ResourceName, r.ResourceDeleted), r.ProjectName, formatAmount(r.Amount), report.Currency)
	}
	if len(projects) > 1 {
		b.WriteString("\nor name its project with --project")
	}
	return "", fmt.Errorf("%s", b.String())
}

// costEntities and costGrains are the --group-by words, each mapped to the
// API's. "type" is the CLI's word for resource_type, short enough to type.
var (
	costEntities = map[string]string{"project": "project", "resource": "resource", "type": "resource_type"}
	costGrains   = map[string]string{"hour": "hour", "day": "day", "month": "month"}
)

// costGroupBy reads --group-by into the API's words, refusing two of one axis
// before the request rather than after it. Unset is "type", which is what this
// command has always shown.
func costGroupBy(cmd *cobra.Command) ([]string, error) {
	raw, _ := cmd.Flags().GetString("group-by")
	if raw == "" {
		raw = "type"
	}
	var entity, grain string
	for _, w := range strings.Split(raw, ",") {
		w = strings.TrimSpace(w)
		switch {
		case costEntities[w] != "" && entity == "":
			entity = costEntities[w]
		case costGrains[w] != "" && grain == "":
			grain = costGrains[w]
		case costEntities[w] != "" || costGrains[w] != "":
			return nil, fmt.Errorf("--group-by takes one of project, resource, type and one of hour, day, month; %q is a second of one", w)
		default:
			return nil, fmt.Errorf("--group-by: %q is not one of project, resource, type, hour, day, month", w)
		}
	}
	var out []string
	for _, a := range []string{entity, grain} {
		if a != "" {
			out = append(out, a)
		}
	}
	return out, nil
}

// costRows lays a report out by its grouping: the keys asked for, then the
// amount, shown to the cent. The report's own amounts are unrounded so that
// they sum to its total; rounding here is display only, and -o json carries
// them as they came.
func costRows(r *client.CostReport) ([]string, [][]string) {
	has := map[string]bool{}
	for _, g := range r.GroupBy {
		has[g] = true
	}
	var headers []string
	switch {
	case has["hour"]:
		headers = append(headers, "HOUR")
	case has["day"]:
		headers = append(headers, "DAY")
	case has["month"]:
		headers = append(headers, "MONTH")
	}
	if has["project"] || has["resource"] {
		headers = append(headers, "PROJECT")
	}
	if has["resource"] {
		headers = append(headers, "RESOURCE", "KIND")
	}
	if has["resource_type"] {
		headers = append(headers, "TYPE", "QUANTITY", "UNIT", "UNIT PRICE")
	}
	headers = append(headers, "AMOUNT")

	rows := make([][]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		var cells []string
		if row.PeriodStart != nil {
			at := row.PeriodStart.UTC()
			switch {
			case has["hour"]:
				cells = append(cells, at.Format("2006-01-02 15:04 MST"))
			case has["day"]:
				cells = append(cells, at.Format("2006-01-02"))
			default:
				cells = append(cells, at.Format("2006-01"))
			}
		}
		if has["project"] || has["resource"] {
			cells = append(cells, deletedName(row.ProjectName, row.ProjectDeleted))
		}
		if has["resource"] {
			name := deletedName(row.ResourceName, row.ResourceDeleted)
			if row.ResourceID == "" {
				// Usage measured on the project and no resource of it. Named,
				// so the rows still visibly add up to the project.
				name = "(project)"
			}
			kind := row.Kind
			if kind == "" {
				kind = "unattributed"
			}
			cells = append(cells, name, kind)
		}
		if has["resource_type"] {
			cells = append(cells, row.ResourceType, formatQuantity(row.Quantity), row.Unit, row.UnitPrice)
		}
		rows = append(rows, append(cells, formatAmount(row.Amount)))
	}
	return headers, rows
}

// deletedName tags a name the record still holds for a project or resource
// that is gone, since a live one may carry the same name.
func deletedName(name string, deleted bool) string {
	if deleted {
		return name + " (deleted)"
	}
	return name
}

// formatAmount shows an unrounded decimal amount to the cent.
func formatAmount(amount string) string {
	v, ok := new(big.Rat).SetString(amount)
	if !ok {
		return amount
	}
	return v.FloatString(2)
}

// costBooks says which rate cards priced the window, and over which part of
// it when there was more than one.
func costBooks(books []client.CostPriceBook) string {
	switch len(books) {
	case 0:
		return ""
	case 1:
		return "Price book: " + books[0].PriceBook
	}
	parts := make([]string, len(books))
	for i, b := range books {
		parts[i] = fmt.Sprintf("%s from %s", b.PriceBook, b.From.UTC().Format("2006-01-02 15:04 MST"))
	}
	return "Price books: " + strings.Join(parts, ", then ")
}

var billingPricesCmd = &cobra.Command{
	Use:   "prices",
	Short: "Show what each resource costs",
	Long: `What a unit of each metered resource costs.

Rates are per unit of whatever the resource is metered in — a core-hour of CPU,
a GiB-hour of memory or storage. Multiply by what ` + "`fpcloud usage`" + ` reports to
get what ` + "`fpcloud billing cost`" + ` will say.

By default this shows YOUR rates: an org can be billed against a rate card other
than the published one, and its invoices use that card rather than the price
page. ` + "`--published`" + ` shows the public list instead, which needs no account and no
billing role — a price is a published fact, and you have to be able to cost a
database before running one.

The book is printed with the rates. The two are one fact: the same usage totals
differently under a different card, so a rate quoted without its book cannot be
checked against anything.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		published, _ := cmd.Flags().GetBool("published")
		c := getClient()

		show := func(book string, prices []*client.Price, payload any) {
			rows := make([][]string, len(prices))
			for i, p := range prices {
				rows[i] = []string{p.ResourceType, p.UnitPrice + " " + p.Currency}
			}
			render([]string{"RESOURCE", "UNIT PRICE"}, rows, payload)
			if !isStructured(rootCmd.Flag("output").Value.String()) {
				fmt.Println(mutedStyle.Render("  Price book: " + book))
			}
		}

		if !published {
			// A caller with no org — not logged in, or logged in with none yet —
			// still gets an answer, because the published list is a fair one for
			// someone who has no book of their own. The label says which it is.
			if orgID, err := resolveOrgID(cmd); err == nil {
				var own *client.OrgPriceList
				withSpinner("Fetching prices...", func() {
					own, err = c.OrgPrices(cmd.Context(), orgID)
				})
				if err != nil {
					return err
				}
				show(own.PriceBook, own.Prices, own)
				return nil
			}
		}

		var prices []*client.Price
		var err error
		withSpinner("Fetching prices...", func() {
			prices, err = c.ListPrices(cmd.Context())
		})
		if err != nil {
			return err
		}
		show("list (published)", prices, prices)
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

A month is invoiced once its usage is all metered, from 06:00 UTC on the first
of the next month, and the invoice never changes after that. Usage that arrives
after its month was invoiced is billed on the next invoice, on a line whose
MONTH is the month it was used in.

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
		headers, rows := invoiceRows(invoices)
		render(headers, rows, invoices)
		return nil
	},
}

// invoiceRows lists the invoices, and when any carries a credit
// says per month what the usage cost, what the credit took off and what is
// due, since under a credit the total alone reads as a run of zeros. With no
// credited invoice the list stays a total.
func invoiceRows(invoices []*client.Invoice) ([]string, [][]string) {
	credited := false
	for _, inv := range invoices {
		if inv.Credit != "" {
			credited = true
			break
		}
	}
	rows := make([][]string, len(invoices))
	for i, inv := range invoices {
		period := inv.PeriodStart.UTC().Format("2006-01")
		if !credited {
			rows[i] = []string{period, inv.Status, inv.Total + " " + inv.Currency, inv.ID}
			continue
		}
		credit := inv.Credit
		if credit == "" {
			credit = "0.00"
		}
		usage := new(big.Rat)
		if t, ok := new(big.Rat).SetString(inv.Total); ok {
			if c, ok := new(big.Rat).SetString(credit); ok {
				usage = t.Add(t, c)
			}
		}
		rows[i] = []string{period, inv.Status, usage.FloatString(2) + " " + inv.Currency,
			"-" + formatAmount(credit) + " " + inv.Currency, inv.Total + " " + inv.Currency, inv.ID}
	}
	if !credited {
		return []string{"PERIOD", "STATUS", "TOTAL", "ID"}, rows
	}
	return []string{"PERIOD", "STATUS", "USAGE", "CREDIT", "DUE", "ID"}, rows
}

var billingCreditCmd = &cobra.Command{
	Use:   "credit",
	Short: "Show the credit your organization holds this month",
	Long: `The credit your organization holds for the month in progress: an amount each
month's invoice is taken down by, why, and the months it covers.

Usage is metered and priced at your rates as for everyone; the credit is one
line at the bottom of the invoice, capped at what the month cost, so usage
above it is what you are invoiced. It covers whole months: an unused part
lapses with its month and nothing carries over. The platform operator grants
it; nothing here sets it.

` + "`fpcloud billing cost`" + ` shows how much of this month's credit the usage so far
has used, what is left, and when it runs out at this month's pace.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		orgID, err := resolveOrgID(cmd)
		if err != nil {
			return err
		}
		c := getClient()
		var credit *client.OrgCredit
		withSpinner("Fetching credit...", func() {
			credit, err = c.GetOrgCredit(cmd.Context(), orgID)
		})
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			// Not an error: most organizations hold none, and the read says so.
			if isStructured(rootCmd.Flag("output").Value.String()) {
				render(nil, nil, nil)
				return nil
			}
			fmt.Println("No credit this month.")
			return nil
		}
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			render(nil, nil, credit)
			return nil
		}
		fmt.Printf("Credit: %s %s a month\n", formatAmount(credit.Amount), credit.Currency)
		if credit.Reason != "" {
			fmt.Printf("Reason: %s\n", credit.Reason)
		}
		fmt.Printf("Since:  %s\n", credit.EffectiveFrom.UTC().Format("January 2006"))
		if credit.EffectiveTo != nil {
			// The month a credit ends is the first it no longer covers; the
			// last covered month is the one a person reads it as.
			fmt.Printf("Until:  %s (last month covered)\n", credit.EffectiveTo.UTC().AddDate(0, -1, 0).Format("January 2006"))
		} else {
			fmt.Println("Until:  withdrawn")
		}
		fmt.Println(mutedStyle.Render("An unused part lapses with its month; nothing carries over."))
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
		month := l.UsagePeriodStart.UTC().Format("2006-01")
		// The credit taken off the invoice (ADR-276): no resource, no
		// quantity, no rate, so those cells say nothing rather than
		// something that is not there.
		if l.Kind == "credit" {
			rows[i] = []string{month, "", "credit: " + l.Description, "", "", "", l.Amount}
			continue
		}
		project := l.ProjectName
		if project == "" {
			project = "(org)"
		}
		rows[i] = []string{month, project, l.ResourceType, formatQuantity(l.Quantity), l.Unit, l.UnitPrice, l.Amount}
	}
	// The unit price is a column, not a footnote: it is the rate this line
	// was BILLED at, stored on the invoice, and it is what a dispute is
	// settled against. A period spanning a price change shows the same
	// resource twice, once per rate. MONTH is the month the usage belongs
	// to, which differs from the invoice's own for usage that arrived after
	// its month was invoiced (ADR-269).
	render([]string{"MONTH", "PROJECT", "RESOURCE", "QUANTITY", "UNIT", "UNIT PRICE", "AMOUNT"}, rows, inv)

	fmt.Printf("\n  %s — %s\n", inv.PeriodStart.UTC().Format("2006-01-02"), inv.PeriodEnd.UTC().Format("2006-01-02"))
	fmt.Printf("  Total: %s %s (%s)\n", inv.Total, inv.Currency, inv.Status)
	return nil
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
	billingCostCmd.Flags().String("group-by", "", "Up to one of project, resource, type and one of hour, day, month (default type)")
	billingCostCmd.Flags().String("resource", "", "One app, database, job, runner or bucket, by name or id")

	billingBudgetSetCmd.Flags().String("amount", "", "Target spend for the period, e.g. 250 (required)")
	billingBudgetSetCmd.Flags().String("currency", "", "Currency (default EUR)")
	billingBudgetSetCmd.Flags().IntSlice("alert-at", nil, "Percentages to alert at (default 50,90,100)")
	billingBudgetCmd.AddCommand(billingBudgetShowCmd, billingBudgetSetCmd, billingBudgetUnsetCmd)

	billingPricesCmd.Flags().Bool("published", false, "Show the public price list rather than your org's own rates")
	billingCmd.AddCommand(billingCostCmd, billingPricesCmd, billingInvoicesCmd, billingCreditCmd, billingBudgetCmd)
	rootCmd.AddCommand(billingCmd)
}

// estimateFlag is `--estimate` on every command that sizes something: print
// what the spec would cost at the organization's rates and stop, creating or
// changing nothing. The same body the command would send is what is
// quoted, so a quote never drifts from the command.
func estimateFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("estimate", false, "Print what this would cost a month at your organization's rates, and do nothing")
}

// estimating reports whether the command was asked only to quote.
func estimating(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("estimate")
	return v
}

// printQuote prints a quote the way `billing cost` prints cost: one row per
// resource type with its quantity an hour, its rate and its month, then the
// range and what the number leaves out. A quantity is a range when the spec
// is one, so an app that sleeps reads "0 – 1.5" and a fixed size one number.
func printQuote(q *client.Quote) error {
	if isStructured(rootCmd.Flag("output").Value.String()) {
		return renderData(q)
	}
	span := func(lo, hi string) string {
		if lo == hi {
			return hi
		}
		return lo + " – " + hi
	}
	headers := []string{"RESOURCE", "PER HOUR", "UNIT", "RATE", "PER MONTH"}
	if q.Hours == 0 {
		headers = []string{"RESOURCE", "PER RUN-HOUR", "UNIT", "RATE"}
	}
	rows := make([][]string, 0, len(q.Lines))
	for _, l := range q.Lines {
		rate, month := l.UnitPrice, span(l.AmountMin, l.AmountMax)
		if rate == "" {
			rate, month = mutedStyle.Render("not priced"), mutedStyle.Render("not in the total")
		}
		row := []string{l.ResourceType, span(l.QuantityMin, l.QuantityMax), l.Unit, rate}
		if q.Hours != 0 {
			row = append(row, month)
		}
		rows = append(rows, row)
	}
	renderTable(headers, rows)
	fmt.Println()
	if q.Hours == 0 {
		fmt.Printf("  %s %s per hour a run takes\n", span(q.HourlyMin, q.HourlyMax), q.Currency)
	} else {
		fmt.Printf("  Estimate: %s %s a month (%d hours), %s %s an hour\n", span(q.TotalMin, q.TotalMax), q.Currency, q.Hours, span(q.HourlyMin, q.HourlyMax), q.Currency)
	}
	fmt.Printf("  Price book: %s\n", q.PriceBook)
	for _, u := range q.Unpriced {
		fmt.Println(mutedStyle.Render(fmt.Sprintf("  %s has no rate in this book yet and is not in the number; it is not free.", u)))
	}
	if len(q.Accrues) > 0 {
		fmt.Println(mutedStyle.Render(fmt.Sprintf("  On top, as it grows: %s.", strings.Join(q.Accrues, ", "))))
	}
	fmt.Println(mutedStyle.Render("  Today's rates for the hours it runs; the invoice bills what actually ran."))
	return nil
}

// firstInt32 is the bound a scale names, or the one the app keeps.
func firstInt32(named *int32, kept int32) *int32 {
	if named != nil {
		return named
	}
	return &kept
}
