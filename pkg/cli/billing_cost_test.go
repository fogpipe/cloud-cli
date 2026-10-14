package cli

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
)

func groupByCmd(value string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("group-by", "", "")
	_ = cmd.Flags().Set("group-by", value)
	return cmd
}

// --group-by takes the CLI's short word for a resource type and the API's
// words for the rest, at most one of each axis, and unset is the by-type view
// this command has always shown (ADR-273).
func TestCostGroupByMapsAndBoundsTheAxes(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"", []string{"resource_type"}},
		{"type", []string{"resource_type"}},
		{"project,day", []string{"project", "day"}},
		{"hour, resource", []string{"resource", "hour"}},
		{"month", []string{"month"}},
	} {
		got, err := costGroupBy(groupByCmd(c.in))
		if err != nil {
			t.Errorf("costGroupBy(%q): %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("costGroupBy(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"project,resource", "day,hour", "app", "resource_type"} {
		if _, err := costGroupBy(groupByCmd(bad)); err == nil {
			t.Errorf("costGroupBy(%q) accepted, want a refusal before the request", bad)
		}
	}
}

// A row shows the keys asked for and the amount to the cent; usage measured
// on no resource of its project is named rather than left blank, so the rows
// visibly add up to the project.
func TestCostRowsShowTheGroupingAskedFor(t *testing.T) {
	day := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	r := &client.CostReport{
		GroupBy: []string{"resource", "day"},
		Rows: []client.CostRow{
			{ProjectName: "shop", ResourceID: "a1", ResourceName: "api", PeriodStart: &day, Amount: "1.234999"},
			{ProjectName: "shop", PeriodStart: &day, Amount: "0.005"},
		},
	}
	headers, rows := costRows(r)
	if want := []string{"DAY", "PROJECT", "RESOURCE", "AMOUNT"}; !reflect.DeepEqual(headers, want) {
		t.Fatalf("headers = %v, want %v", headers, want)
	}
	if want := []string{"2026-10-03", "shop", "api", "1.23"}; !reflect.DeepEqual(rows[0], want) {
		t.Errorf("row 0 = %v, want %v", rows[0], want)
	}
	if want := []string{"2026-10-03", "shop", "(project)", "0.01"}; !reflect.DeepEqual(rows[1], want) {
		t.Errorf("row 1 = %v, want %v", rows[1], want)
	}

	byType := &client.CostReport{
		GroupBy: []string{"resource_type"},
		Rows:    []client.CostRow{{ResourceType: "compute.cpu", Unit: "core-hour", UnitPrice: "0.012", Quantity: "100", Amount: "1.2"}},
	}
	headers, rows = costRows(byType)
	if want := []string{"TYPE", "QUANTITY", "UNIT", "UNIT PRICE", "AMOUNT"}; !reflect.DeepEqual(headers, want) {
		t.Errorf("by-type headers = %v, want %v", headers, want)
	}
	if want := []string{"compute.cpu", "100.00", "core-hour", "0.012", "1.20"}; !reflect.DeepEqual(rows[0], want) {
		t.Errorf("by-type row = %v, want %v", rows[0], want)
	}
}

// --resource resolves a name among the resources that cost something in the
// period, so a deleted one still answers; an id is taken as given. A name two
// resources share is refused with each one's id, and --project is suggested
// only when they sit in different projects: in one project, a name taken again
// after a delete, it cannot help.
func TestCostResourceIDResolvesANameFromTheRecord(t *testing.T) {
	report := &client.CostReport{Currency: "EUR", Rows: []client.CostRow{
		{ProjectName: "shop", ResourceID: "a1", ResourceName: "web", Amount: "1"},
		{ProjectName: "shop", ResourceID: "d1", ResourceName: "db", Amount: "1.3904"},
		{ProjectName: "shop", ResourceID: "d0", ResourceName: "db", Amount: "0.001"},
		{ProjectName: "blog", ResourceID: "a2", ResourceName: "web", Amount: "1"},
		{ProjectName: "shop", Amount: "1"},
	}}
	if id, err := costResourceID(report, "d1"); err != nil || id != "d1" {
		t.Errorf("costResourceID(d1) = %q, %v; want d1", id, err)
	}
	if _, err := costResourceID(report, "gone"); err == nil {
		t.Error("a name with no cost resolved")
	}

	_, err := costResourceID(report, "web")
	if err == nil {
		t.Fatal("a name two projects share resolved; want a refusal")
	}
	for _, want := range []string{"a1  web in shop, 1.00 EUR", "a2  web in blog, 1.00 EUR", "--project"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %q", err, want)
		}
	}

	_, err = costResourceID(report, "db")
	if err == nil {
		t.Fatal("a name one project holds twice resolved; want a refusal")
	}
	for _, want := range []string{"d1  db in shop, 1.39 EUR", "d0  db in shop, 0.00 EUR"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "--project") {
		t.Errorf("refusal %q suggests --project, which cannot tell two of one project's resources apart", err)
	}
}
