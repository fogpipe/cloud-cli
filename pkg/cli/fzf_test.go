package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// fakeFzf writes a stand-in for fzf that prints the line at index pick and
// exits with code. It reads the same stdin the real fzf would, so the row the
// picker built is the row that comes back.
func fakeFzf(t *testing.T, pick int, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the picker shells out to fzf; the stand-in is a POSIX script")
	}
	path := filepath.Join(t.TempDir(), "fzf")
	script := fmt.Sprintf("#!/bin/sh\nlines=$(cat)\nif [ %d -ne 0 ]; then exit %d; fi\nprintf '%%s\\n' \"$lines\" | sed -n '%dp'\n", code, code, pick+1)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func orgFixtures() []*client.Organization {
	// Deliberately mixed lengths. Every entry but the longest is padded in the
	// rendered row, and a fixture where all names are the same length passes
	// even when the padding is never removed.
	return []*client.Organization{
		{ID: "id-fp", ShortID: "fp", DisplayName: "Fogpipe"},
		{ID: "id-ixdev", ShortID: "ixdev", DisplayName: "ixdev"},
		{ID: "id-rkv", ShortID: "rkv", DisplayName: "Rymdkraftverk"},
		{ID: "id-kruso", ShortID: "kruso", DisplayName: "Kruso"},
	}
}

func TestPickOrgFzfResolvesEveryEntry(t *testing.T) {
	orgs := orgFixtures()
	for i, want := range orgs {
		got, err := pickOrgFzf(fakeFzf(t, i, 0), orgs)
		if err != nil {
			t.Fatalf("%s: %v", want.ShortID, err)
		}
		if got == nil {
			t.Fatalf("%s: picker resolved nothing, which the caller reports as a cancellation", want.ShortID)
		}
		if got.ID != want.ID {
			t.Fatalf("picked %q, got %q", want.ShortID, got.ShortID)
		}
	}
}

func TestPickProjectFzfResolvesEveryEntry(t *testing.T) {
	projects := []*client.Project{
		{Name: "api", Egress: "restricted"},
		{Name: "boilerplate", Egress: "all"},
		{Name: "web", Egress: "https", IsPlatform: true},
	}
	for i, want := range projects {
		got, err := pickProjectFzf(fakeFzf(t, i, 0), projects)
		if err != nil {
			t.Fatalf("%s: %v", want.Name, err)
		}
		if got != want.Name {
			t.Fatalf("picked %q, got %q", want.Name, got)
		}
	}
}

// A cancelled picker and a selection that resolves to nothing are different
// events, and reporting both as "cancelled" is what hid the padding defect.
func TestFzfPickCancelIsNotAnError(t *testing.T) {
	i, ok, err := fzfPick(fakeFzf(t, 0, 130), "p> ", "h", []string{"a", "b"})
	if err != nil || ok || i != 0 {
		t.Fatalf("cancel: got i=%d ok=%v err=%v", i, ok, err)
	}
}

func TestFzfPickUnresolvableSelectionIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fzf")
	// Prints a row that carries no index column at all.
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat >/dev/null\necho 'not-an-index\tlabel'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, ok, err := fzfPick(path, "p> ", "h", []string{"a", "b"})
	if err == nil {
		t.Fatal("a selection naming no row must be an error, not a silent cancellation")
	}
	if ok {
		t.Fatal("ok must be false when the selection cannot be resolved")
	}
	if !strings.Contains(err.Error(), "naming no row") {
		t.Fatalf("error should say what happened, got %v", err)
	}
}
