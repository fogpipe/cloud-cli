package cli

import (
	"strings"
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// The header is ordered checks-not-made, faults, then what waits on the
// tenant, whatever order the document lists the resources in.
func TestAttentionOrdersUncheckedThenFaultsThenWaiting(t *testing.T) {
	s := &client.ProjectStatus{
		Domains: []client.DomainStatus{{Domain: "shop.example", Problems: []client.StatusProblem{
			{Reason: "AwaitingDNS", Class: client.ProblemWaiting, Detail: "TXT record missing"},
		}}},
		Apps: []client.AppStatus{{ID: "a1", Name: "api", Problems: []client.StatusProblem{
			{Reason: "CrashLoopBackOff", Class: client.ProblemFault, Count: 14},
		}}},
		Unchecked: []client.UncheckedStatus{{Check: "health", Error: "cluster unreachable"}},
	}
	rows := attentionRows(s)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want 3", rows)
	}
	if rows[0].rank != rankUnchecked || rows[1].name != "api" || rows[2].name != "shop.example" {
		t.Fatalf("order = %+v", rows)
	}
	out := renderAttention(s)
	for _, want := range []string{"Needs attention (3)", "app api", "CrashLoopBackOff (×14)", "domain shop.example", "AwaitingDNS: TXT record missing", "(health): cluster unreachable"} {
		if !strings.Contains(out, want) {
			t.Errorf("header lacks %q:\n%s", want, out)
		}
	}
}

// Three resources of one kind failing one way are one line, so a fault
// across a fleet reads as one fault.
func TestAttentionCollapsesOneReasonAcrossThree(t *testing.T) {
	crash := []client.StatusProblem{{Reason: "CrashLoopBackOff", Class: client.ProblemFault}}
	s := &client.ProjectStatus{Apps: []client.AppStatus{
		{Name: "a", Problems: crash}, {Name: "b", Problems: crash}, {Name: "c", Problems: crash},
		{Name: "d", Problems: []client.StatusProblem{{Reason: "ImageMissing", Class: client.ProblemFault}}},
	}}
	out := renderAttention(s)
	if !strings.Contains(out, "Needs attention (2)") || !strings.Contains(out, "3 apps") {
		t.Fatalf("not collapsed:\n%s", out)
	}
}

// A clean document says what was checked, and a bucket is never in that,
// since nothing checks one.
func TestAttentionCleanNamesWhatWasChecked(t *testing.T) {
	s := &client.ProjectStatus{
		Apps:    []client.AppStatus{{Name: "a"}, {Name: "b"}},
		Buckets: []client.BucketStatus{{Name: "files"}},
		Runner:  &client.RunnerStatus{},
	}
	out := renderAttention(s)
	if !strings.Contains(out, "Nothing needs attention") || !strings.Contains(out, "checked 2 apps and the runner") {
		t.Fatalf("clean header = %q", out)
	}
	if strings.Contains(out, "bucket") {
		t.Fatalf("a bucket was counted as checked: %q", out)
	}
}

// A document carrying a check that was not made has no clean verdict.
func TestAttentionUncheckedIsNeverClean(t *testing.T) {
	s := &client.ProjectStatus{Unchecked: []client.UncheckedStatus{{Check: "workloads", Error: "timeout"}}}
	if out := renderAttention(s); strings.Contains(out, "Nothing needs attention") {
		t.Fatalf("incomplete report read as clean: %q", out)
	}
}
