package cli

import (
	"testing"
	"time"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// The trap this guards is the reason `registry scan` can follow a rescan at all
// (ADR-214): an image scanned last week is ALREADY in ScanStateScanned when the
// command starts, so "wait until it stops being pending" hands back the old
// verdict instantly and presents it as the answer to the scan just asked for.
func TestAScanFromBeforeTheDispatchIsNotTheAnswer(t *testing.T) {
	dispatched := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	lastWeek := dispatched.Add(-7 * 24 * time.Hour)

	stale := &client.RegistryCVEList{State: client.ScanStateScanned, ScannedAt: &lastWeek}
	if scanReportedSince(stale, &dispatched) {
		t.Fatal("a scan from before the dispatch was accepted as its result, so a rescan reports the verdict it was asked to replace")
	}

	// The control: the same record, scanned after the dispatch, MUST be taken.
	// Without it a followScan that never accepts anything would pass the test
	// above while being completely broken.
	fresh := dispatched.Add(14 * time.Second)
	settled := &client.RegistryCVEList{State: client.ScanStateScanned, ScannedAt: &fresh}
	if !scanReportedSince(settled, &dispatched) {
		t.Fatal("a scan from after the dispatch was rejected, so following one would wait out the full timeout")
	}
}

// A scan recorded at exactly the dispatch instant counts. The two timestamps
// come from different clocks and the difference is meaningless at that
// resolution; excluding it would hang until the timeout on a result that is
// already there.
func TestAScanAtTheDispatchInstantCounts(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	if !scanReportedSince(&client.RegistryCVEList{State: client.ScanStateScanned, ScannedAt: &at}, &at) {
		t.Fatal("a scan recorded at the dispatch instant was rejected")
	}
}

// Pending is never an answer, whatever the timestamps say: it is the state an
// image sits in from before the dispatch until the Job reports.
func TestPendingIsNeverTheAnswer(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	after := at.Add(time.Minute)
	for _, c := range []struct {
		name string
		list *client.RegistryCVEList
	}{
		{"no scan time", &client.RegistryCVEList{State: client.ScanStatePending}},
		{"a later scan time", &client.RegistryCVEList{State: client.ScanStatePending, ScannedAt: &after}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if scanReportedSince(c.list, &at) {
				t.Fatal("pending was taken as a reported scan")
			}
		})
	}
}

// A failed or refused scan IS a result — the ask is over, and waiting longer
// would report a settled outcome as one still running.
func TestASettledFailureEndsTheFollow(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	after := at.Add(time.Minute)
	for _, state := range []string{client.ScanStateFailed, client.ScanStateRefused} {
		t.Run(state, func(t *testing.T) {
			if !scanReportedSince(&client.RegistryCVEList{State: state, ScannedAt: &after}, &at) {
				t.Fatalf("%s was not taken as a result, so the follow would wait out its timeout", state)
			}
		})
	}
}
