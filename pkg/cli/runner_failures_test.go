package cli

import (
	"reflect"
	"testing"
	"time"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func TestRunnerFailureRowsNameTheJobAndLeaveAPodsCodeBlank(t *testing.T) {
	code := 137
	at := time.Date(2026, 9, 15, 7, 25, 30, 0, time.UTC)
	got := runnerFailureRows([]client.RunnerFailure{
		{At: at, Pod: "p1", Container: "runner", Reason: "OOMKilled", ExitCode: &code, MemoryLimit: "4Gi",
			Job: "full", Repository: "acme/web", WorkflowRunID: 34940962988},
		{At: at.Add(-time.Hour), Pod: "p2", Reason: "PreemptionByScheduler"},
	})
	want := [][]string{
		{"2026-09-15 07:25 UTC", "runner", "OOMKilled", "137", "4Gi", "full (acme/web, run 34940962988)"},
		{"2026-09-15 06:25 UTC", "(pod)", "PreemptionByScheduler", "—", "—", "—"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}
