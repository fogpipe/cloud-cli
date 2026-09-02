package cli

import (
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func testOrgs() []*client.Organization {
	return []*client.Organization{
		{ID: "11111111-1111-1111-1111-111111111111", ShortID: "rkv-a1b2", DisplayName: "Rymdkraftverk"},
		{ID: "22222222-2222-2222-2222-222222222222", ShortID: "acme-c3d4", DisplayName: "Acme", FKEEnabled: true},
	}
}

// An org answers to its uuid, its frozen short id and its readable name, and a
// reference is matched case-insensitively on the last of those.
func TestMatchOrg_EveryReference(t *testing.T) {
	orgs := testOrgs()
	for _, ref := range []string{
		"11111111-1111-1111-1111-111111111111",
		"rkv-a1b2",
		"Rymdkraftverk",
		"rymdkraftverk",
	} {
		if got := matchOrg(orgs, ref); got == nil || got.ShortID != "rkv-a1b2" {
			t.Errorf("matchOrg(%q) = %v, want rkv-a1b2", ref, got)
		}
	}
	if got := matchOrg(orgs, "nope"); got != nil {
		t.Errorf("matchOrg(unknown) = %v, want nil", got)
	}
}

// The stored context is the frozen short id, never the reference as typed: a
// display name is mutable (ADR-094), so a config holding one stops resolving the
// moment the org is renamed.
func TestApplyOrg_StoresFrozenShortID(t *testing.T) {
	orgs := testOrgs()
	cfg := &Config{}
	applyOrg(cfg, matchOrg(orgs, "Rymdkraftverk"))
	if cfg.CurrentOrg != "rkv-a1b2" {
		t.Errorf("CurrentOrg = %q, want the short id rkv-a1b2", cfg.CurrentOrg)
	}
	if cfg.CurrentOrgFKE {
		t.Error("CurrentOrgFKE should follow the org's entitlement")
	}
	applyOrg(cfg, matchOrg(orgs, "acme-c3d4"))
	if cfg.CurrentOrg != "acme-c3d4" || !cfg.CurrentOrgFKE {
		t.Errorf("switching org left %+v", cfg)
	}
}

// `switch` takes the org first and the project second, so neither argument can
// be read as the other and no disambiguation rule is needed.
func TestSwitchCmd_ArgShape(t *testing.T) {
	if switchCmd.Use != "switch [org] [project]" {
		t.Errorf("Use = %q", switchCmd.Use)
	}
	for _, n := range []int{0, 1, 2} {
		if err := switchCmd.Args(switchCmd, make([]string, n)); err != nil {
			t.Errorf("%d args rejected: %v", n, err)
		}
	}
	if err := switchCmd.Args(switchCmd, make([]string, 3)); err == nil {
		t.Error("3 args accepted, want a usage error")
	}
}

// The `use` spelling is deleted rather than aliased (pre-GA: no compatibility
// scaffolding), so `org use` and `project use` must name no command.
func TestUseSpellingIsGone(t *testing.T) {
	for _, args := range [][]string{{"org", "use"}, {"project", "use"}} {
		cmd, rest, err := rootCmd.Find(args)
		if err == nil && len(rest) == 0 && cmd.Name() == "use" {
			t.Errorf("%v still resolves to a command", args)
		}
	}
	for _, args := range [][]string{{"switch"}, {"org", "switch"}, {"project", "switch"}} {
		cmd, _, err := rootCmd.Find(args)
		if err != nil || cmd.Name() != "switch" {
			t.Errorf("%v does not resolve to switch: %v", args, err)
		}
	}
}
