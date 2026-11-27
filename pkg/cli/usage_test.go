package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func f(v float64) *float64 { return &v }

// Each dimension is a row in its own unit. A figure the platform could not read
// says so, a measured zero is 0, and a figure the dimension does not have is
// "-": backups reserve nothing ahead, and a count has no use over time
// (docs/reading-a-zero.md, ADR-279).
func TestCapacityRowsSayWhatEachDimensionHolds(t *testing.T) {
	hour := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	later := hour.Add(time.Hour)
	r := &client.CapacityReport{Scope: "organization", Axes: []client.CapacityAxis{
		{Axis: "cpu", Bounded: true, Unit: "core", Reserves: true, MeasuresUse: true, Allowed: f(8), Counts: "booked", Counted: f(2.5), Reserved: f(1), ReservedHour: &hour, Used: f(0.016), UsedHour: &later, Unmeasured: []string{"job", "runner"}},
		{Axis: "memory", Bounded: true, Unit: "byte", Reserves: true, MeasuresUse: true, Allowed: f(16 << 30), Counts: "booked", Counted: nil, Reserved: f(2 << 30), ReservedHour: &hour, Used: nil},
		{Axis: "backup_storage", Bounded: true, Unit: "byte", MeasuresUse: true, Allowed: f(80 << 30), Counts: "stored", Counted: f(0), Used: f(0), UsedHour: &hour},
		{Axis: "log_bytes_per_second", Bounded: true, Unit: "byte_per_second", MeasuresUse: true, Allowed: f(20480), Counts: "written", Counted: f(1536), Used: f(1536), UsedHour: &hour},
		{Axis: "http_requests", Unit: "request_per_hour", MeasuresUse: true, Used: f(1204.4), UsedHour: &hour},
		{Axis: "http_requests", Unit: "request_per_hour", MeasuresUse: true, Used: f(0.42), UsedHour: &hour},
		{Axis: "pods", Bounded: true, Unit: "count", Allowed: f(20), Counts: "booked", Counted: f(3), OrgBound: "below"},
	}}
	headers, rows := capacityRows(r)
	assert.Equal(t, []string{"DIMENSION", "ALLOWED", "COUNTED", "RESERVED", "USED", "HOUR", "NOT MEASURED"}, headers)
	assert.Equal(t, [][]string{
		{"CPU", "8", "2.5 booked", "1", "16m", "Nov 16 12:00 / Nov 16 13:00", "jobs, runners"},
		{"Memory", "16Gi", "no reading", "2Gi", "no reading", "Nov 16 12:00", ""},
		{"Backups", "80Gi", "0B stored", "-", "0B", "Nov 16 12:00", ""},
		{"Logs", "20Ki/s", "1.5Ki/s written", "-", "1.5Ki/s", "Nov 16 12:00", ""},
		{"HTTP requests", "-", "-", "-", "1204/h", "Nov 16 12:00", ""},
		{"HTTP requests", "-", "-", "-", "0.4/h", "Nov 16 12:00", ""},
		{"Instances", "20", "3 booked", "-", "-", "-", ""},
	}, rows)
	assert.Empty(t, capacityNear(r), "an organization read says nothing about being near: it shows the bound")
}

// A project read carries no bound, so its table has no column for one, and it
// says which dimensions its organization is near a bound on, and who can ask
// for more.
func TestAProjectsCapacityRowsCarryNoBound(t *testing.T) {
	hour := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	r := &client.CapacityReport{Scope: "project", Axes: []client.CapacityAxis{
		{Axis: "cpu", Unit: "core", Reserves: true, MeasuresUse: true, OrgBound: "near", Counts: "booked", Counted: f(2), Reserved: f(1), ReservedHour: &hour, Used: f(0.016), UsedHour: &hour},
		{Axis: "memory", Unit: "byte", Reserves: true, MeasuresUse: true, OrgBound: "below", Counts: "booked", Reserved: nil, Used: nil},
		{Axis: "pods", Unit: "count", OrgBound: "at", Counts: "booked", Counted: f(4)},
		{Axis: "objects", Unit: "count", Counts: "stored"},
	}}
	headers, rows := capacityRows(r)
	assert.Equal(t, []string{"DIMENSION", "BOOKED", "RESERVED", "USED", "HOUR", "NOT MEASURED"}, headers)
	assert.Equal(t, [][]string{
		{"CPU", "2", "1", "16m", "Nov 16 12:00", ""},
		{"Memory", "no reading", "no reading", "no reading", "-", ""},
		{"Instances", "4", "-", "-", "-", ""},
		{"Objects", "-", "-", "-", "-", ""},
	}, rows)
	assert.Equal(t, "The organization is at its bound on Instances: creates on it are refused.\n"+
		"The organization is near its bound on CPU.\n"+
		"An owner can ask for more with `fpcloud org quota request`.", capacityNear(r))
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

	registry := client.CapacityAxis{Axis: "registry_storage", Unit: "byte", MeasuresUse: true, Counts: "stored", Rows: []client.CapacityRow{{Kind: "project", ID: "p1", Name: "demo", Used: f(1 << 20)}}}
	_, rows = capacityShareRows(registry)
	assert.Equal(t, [][]string{{"project", "demo", "-", "1Mi"}}, rows, "a dimension that reserves nothing ahead, and books nothing")
}

// On a dimension whose bound counts what is booked, --rows says what each
// holder books now, largest first: a runner that never ran books its size, and
// what the platform did not render is named by what it is.
func TestCapacityShareRowsSayWhatEachHolderBooks(t *testing.T) {
	a := client.CapacityAxis{Axis: "cpu", Unit: "core", Reserves: true, MeasuresUse: true, Counts: "booked", Rows: []client.CapacityRow{
		{Kind: "app", ID: "a1", Name: "web", Counted: f(1), Reserved: f(0.25), Used: f(0.01)},
		{Kind: "runner", ID: "r1", Name: "ci", Counted: f(2), Reserved: nil, Used: nil},
		{Kind: "workload", Name: "legacy", Counted: nil},
	}}
	headers, rows := capacityShareRows(a)
	assert.Equal(t, []string{"KIND", "NAME", "BOOKED", "RESERVED", "USED"}, headers)
	assert.Equal(t, [][]string{
		{"runner", "ci", "2", "no reading", "no reading"},
		{"app", "web", "1", "250m", "10m"},
		{"workload", "legacy", "no reading", "no reading", "no reading"},
	}, rows)
}

// A cost total says how far it reaches, so a resource created minutes ago
// reading nothing is explained by the hour, not taken for free.
func TestACostTotalSaysTheHourItIsMeteredThrough(t *testing.T) {
	through := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	assert.Equal(t, "Computed from usage metered through Nov 16 13:00 UTC, not an invoice.", costRecency(&through))
	assert.Contains(t, costRecency(nil), "No hour is metered yet.")
}

// --to is read in the dimension's own spelling into the base unit the wire
// carries, as the capacity read reports a bound.
func TestABoundIsReadInItsDimensionsSpelling(t *testing.T) {
	for _, c := range []struct {
		axis, raw string
		want      float64
	}{
		{"memory", "32Gi", 32 << 30},
		{"object_storage", "500G", 500e9},
		{"cpu", "16", 16},
		{"cpu", "1500m", 1.5},
		{"pods", "40", 40},
	} {
		got, err := parseBound(c.axis, c.raw)
		assert.NoError(t, err, c.raw)
		assert.Equal(t, c.want, got, c.raw)
	}
	for _, bad := range [][2]string{{"pods", "4.5"}, {"memory", "lots"}, {"cpu", "-2"}, {"objects", "0"}} {
		_, err := parseBound(bad[0], bad[1])
		assert.Error(t, err, bad[1])
	}
}
