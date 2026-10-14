package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func f(v float64) *float64 { return &v }

// Each dimension is a row in its own unit. A figure the platform could not read
// says so, a measured zero is 0, and a figure the dimension does not have is
// "-": backups reserve nothing ahead, and a count has no use over time
// (docs/reading-a-zero.md, ADR-279).
func TestCapacityRowsSayWhatEachDimensionHolds(t *testing.T) {
	r := &client.CapacityReport{Axes: []client.CapacityAxis{
		{Axis: "cpu", Unit: "core", Reserves: true, MeasuresUse: true, Allowed: f(8), Admitted: f(2.5), Reserved: f(1), Used: f(0.016), Unmeasured: []string{"job", "runner"}},
		{Axis: "memory", Unit: "byte", Reserves: true, MeasuresUse: true, Allowed: f(16 << 30), Admitted: nil, Reserved: f(2 << 30), Used: nil},
		{Axis: "backup_storage", Unit: "byte", MeasuresUse: true, Allowed: f(80 << 30), Admitted: f(0), Used: f(0)},
		{Axis: "pods", Unit: "count", Allowed: f(20), Admitted: f(3)},
	}}
	headers, rows := capacityRows(r)
	assert.Equal(t, []string{"DIMENSION", "ALLOWED", "ADMITTED", "RESERVED", "USED", "NOT MEASURED"}, headers)
	assert.Equal(t, [][]string{
		{"CPU", "8", "2.5", "1", "16m", "jobs, runners"},
		{"Memory", "16Gi", "no reading", "2Gi", "no reading", ""},
		{"Backups", "80Gi", "0B", "-", "0B", ""},
		{"Instances", "20", "3", "-", "-", ""},
	}, rows)
}

// --rows lists what each project or resource held on a dimension over the
// period: usage on no resource is the project's own, and a deleted resource is
// tagged, since a live one may carry its name.
func TestCapacityShareRowsNameEachHolder(t *testing.T) {
	a := client.CapacityAxis{Axis: "cpu", Unit: "core", Reserves: true, MeasuresUse: true, Rows: []client.CapacityRow{
		{Kind: "app", ID: "a1", Name: "web", Reserved: f(0.25), Used: f(0.01)},
		{Kind: "app", ID: "a0", Name: "web", Deleted: true, Reserved: f(0.1), Used: nil},
		{Kind: "project", Reserved: f(0), Used: nil},
	}}
	headers, rows := capacityShareRows(a)
	assert.Equal(t, []string{"KIND", "NAME", "RESERVED", "USED"}, headers)
	assert.Equal(t, [][]string{
		{"app", "web", "250m", "10m"},
		{"app", "web (deleted)", "100m", "no reading"},
		{"project", "(project)", "0m", "no reading"},
	}, rows)

	registry := client.CapacityAxis{Axis: "registry_storage", Unit: "byte", MeasuresUse: true, Rows: []client.CapacityRow{{Kind: "project", ID: "p1", Name: "demo", Used: f(1 << 20)}}}
	_, rows = capacityShareRows(registry)
	assert.Equal(t, [][]string{{"project", "demo", "-", "1Mi"}}, rows, "a dimension that reserves nothing ahead")
}
