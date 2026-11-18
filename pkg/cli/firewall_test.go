package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// --rule is three fields and nothing else, so every other shape is refused
// with the form spelled out instead of being read as something near it.
func TestParseFirewallRuleFlag(t *testing.T) {
	rule, err := parseFirewallRuleFlag("100 allow 198.51.100.0/24,2001:db8::/32")
	require.NoError(t, err)
	assert.Equal(t, client.FirewallRuleRequest{
		Priority: 100,
		Action:   "allow",
		Match:    client.FirewallMatch{Sources: []string{"198.51.100.0/24", "2001:db8::/32"}},
	}, rule)
	assert.Nil(t, rule.Enabled, "enabled is left to the API's default, which is true")

	rule, err = parseFirewallRuleFlag("  7   deny   203.0.113.7  ")
	require.NoError(t, err, "a bare address is a range, and spacing between fields is free")
	assert.Equal(t, []string{"203.0.113.7"}, rule.Match.Sources)

	for spec, want := range map[string]string{
		"":                                    "0 fields",
		"100 allow":                           "2 fields",
		"100 allow 198.51.100.0/24, 10.0.0.1": "4 fields",
		"100 allow 10.0.0.0/8 office":         "4 fields",
		"first allow 10.0.0.0/8":              `priority "first" is not a number`,
		"100 permit 10.0.0.0/8":               `action "permit" is not allow or deny`,
		"100 ALLOW 10.0.0.0/8":                `action "ALLOW" is not allow or deny`,
		"100 allow 10.0.0.0/33":               `"10.0.0.0/33" is not an address range`,
		"100 allow example.com":               `"example.com" is not an address range`,
		"100 allow 10.0.0.0/8,":               `"" is not an address range`,
		"allow 100 10.0.0.0/8":                `priority "allow" is not a number`,
	} {
		_, err := parseFirewallRuleFlag(spec)
		require.Error(t, err, spec)
		assert.Contains(t, err.Error(), want, spec)
	}

	_, err = parseFirewallRuleFlag("100 allow")
	assert.Contains(t, err.Error(), "<priority> <allow|deny> <cidr>", "a wrong shape is answered with the form")
}

// A priority is a rule's identity, so two --rule at one priority is a mistake
// in the command line, refused before any request.
func TestParseFirewallRuleFlags_APriorityNamesOneRule(t *testing.T) {
	rules, err := parseFirewallRuleFlags([]string{"200 deny 203.0.113.0/24", "100 allow 198.51.100.0/24"})
	require.NoError(t, err)
	require.Len(t, rules, 2)
	assert.Equal(t, 200, rules[0].Priority, "order is as given; the priority orders evaluation, not the list")

	_, err = parseFirewallRuleFlags([]string{"100 allow 10.0.0.0/8", "100 deny 10.1.0.0/16"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "priority 100 is given twice")
	assert.True(t, isUsageErrorValue(err))

	_, err = parseFirewallRuleFlags([]string{"nope"})
	assert.True(t, isUsageErrorValue(err))
}

func isUsageErrorValue(err error) bool {
	var ue usageError
	return errors.As(err, &ue)
}

func TestReadFirewallFile(t *testing.T) {
	write := func(name, content string) string {
		path := filepath.Join(t.TempDir(), name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		return path
	}

	file, err := readFirewallFile(write("fw.yaml", `
description: office and VPN only
default_action: deny
rules:
  - priority: 100
    action: allow
    description: office
    match:
      sources: [198.51.100.0/24, "2001:db8::/32"]
  - priority: 200
    action: deny
    enabled: false
    match:
      sources: [203.0.113.7]
`), nil)
	require.NoError(t, err)
	require.NotNil(t, file.Description)
	assert.Equal(t, "office and VPN only", *file.Description)
	assert.Equal(t, "deny", file.DefaultAction)
	require.Len(t, file.Rules, 2)
	assert.Equal(t, []string{"198.51.100.0/24", "2001:db8::/32"}, file.Rules[0].Match.Sources)
	assert.Nil(t, file.Rules[0].Enabled, "enabled left out stays left out, which the API reads as true")
	require.NotNil(t, file.Rules[1].Enabled)
	assert.False(t, *file.Rules[1].Enabled)

	file, err = readFirewallFile(write("fw.json", `{"default_action":"allow","rules":[{"priority":1,"action":"deny","match":{"sources":["203.0.113.0/24"]}}]}`), nil)
	require.NoError(t, err)
	assert.Nil(t, file.Description, "a file that does not state the description leaves it alone")
	require.Len(t, file.Rules, 1)

	file, err = readFirewallFile("-", strings.NewReader("rules: []\n"))
	require.NoError(t, err)
	assert.Empty(t, file.Rules)

	for content, want := range map[string]string{
		"rules:\n  - priority: 1\n    action: allow\n    match:\n      source: [10.0.0.0/8]\n": "source",
		"default-action: deny\n": "default-action",
		"default_action: drop\n": "default_action is allow or deny",
		"rules:\n  - priority: 1\n    action: reject\n    match:\n      sources: [10.0.0.0/8]\n": "rules[0].action is allow or deny",
		"rules:\n  - priority: 1\n    action: allow\n":                                           "rules[0].match.sources: a rule needs at least one address range",
		"rules:\n  - priority: 1\n    action: allow\n    match:\n      sources: [office]\n":      `"office" is not an address range`,
	} {
		_, err := readFirewallFile(write("bad.yaml", content), nil)
		require.Error(t, err, content)
		assert.Contains(t, err.Error(), want, content)
	}

	_, err = readFirewallFile(filepath.Join(t.TempDir(), "absent.yaml"), nil)
	assert.Error(t, err)
}

// What firewall get prints as JSON or YAML is a firewall file: the fields a
// read adds are ignored, so a firewall can be saved, edited and written back.
func TestReadFirewallFile_AcceptsAFirewallRead(t *testing.T) {
	fw := client.Firewall{
		ID: "fw-1", ProjectID: "p1", Name: "office", Description: "office only",
		Revision: 4, DefaultAction: "deny",
		Rules:     []client.FirewallRule{{Priority: 100, Action: "allow", Enabled: false, Match: client.FirewallMatch{Sources: []string{"198.51.100.0/24"}}}},
		Targets:   []client.FirewallTarget{{Type: "app", ID: "app-1", Name: "web", AppliedRevision: 4, Status: "applied"}},
		Findings:  []client.FirewallFinding{{Code: "NO_IPV6", Field: "default_action", Message: "no IPv6 source is allowed"}},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	data, err := json.Marshal(fw)
	require.NoError(t, err)

	file, err := readFirewallFile("-", strings.NewReader(string(data)))
	require.NoError(t, err)
	assert.Equal(t, "office only", *file.Description)
	assert.Equal(t, "deny", file.DefaultAction)
	require.Len(t, file.Rules, 1)
	require.NotNil(t, file.Rules[0].Enabled)
	assert.False(t, *file.Rules[0].Enabled, "a disabled rule comes back disabled")
}

// A rule change writes the whole firewall back, so what it does not touch has
// to survive the round trip: a disabled rule most of all, since leaving
// enabled out would turn it on.
func TestFirewallUpdateOf_ChangesNothing(t *testing.T) {
	fw := &client.Firewall{
		Description: "office only", DefaultAction: "deny",
		Rules: []client.FirewallRule{
			{Priority: 100, Action: "allow", Description: "office", Enabled: true, Match: client.FirewallMatch{Sources: []string{"198.51.100.0/24"}}},
			{Priority: 200, Action: "deny", Enabled: false, Match: client.FirewallMatch{Sources: []string{"203.0.113.7"}}},
		},
	}
	req := firewallUpdateOf(fw)
	assert.Equal(t, "office only", req.Description)
	assert.Equal(t, "deny", req.DefaultAction)
	assert.Zero(t, req.IfRevision, "the condition is the caller's to state")
	require.Len(t, req.Rules, 2)
	assert.True(t, *req.Rules[0].Enabled)
	assert.False(t, *req.Rules[1].Enabled)
	assert.Equal(t, "office", req.Rules[0].Description)

	req.Rules[0].Match.Sources[0] = "changed"
	assert.Equal(t, "198.51.100.0/24", fw.Rules[0].Match.Sources[0], "the request does not alias the firewall it was read from")
}

func TestFirewallSettled(t *testing.T) {
	target := func(name string, applied int, status, message string) client.FirewallTarget {
		return client.FirewallTarget{Type: "app", Name: name, AppliedRevision: applied, Status: status, Message: message}
	}
	for _, c := range []struct {
		name    string
		targets []client.FirewallTarget
		want    bool
	}{
		{"nothing attached, so nothing to wait for", nil, true},
		{"every target at the revision", []client.FirewallTarget{target("web", 4, "applied", ""), target("api", 4, "applied", "")}, true},
		{"a target past the revision", []client.FirewallTarget{target("web", 5, "applied", "")}, true},
		{"one target behind", []client.FirewallTarget{target("web", 4, "applied", ""), target("api", 3, "applied", "")}, false},
		{"at the revision and still pending", []client.FirewallTarget{target("web", 4, "pending", "")}, false},
		{"never applied", []client.FirewallTarget{target("web", 0, "pending", "")}, false},
	} {
		got, err := firewallSettled(&client.Firewall{Name: "office", Targets: c.targets}, 4)
		require.NoError(t, err, c.name)
		assert.Equal(t, c.want, got, c.name)
	}

	_, err := firewallSettled(&client.Firewall{Name: "office", Targets: []client.FirewallTarget{
		target("web", 4, "applied", ""),
		target("api", 3, "failed", "the edge refused the policy: too many sources"),
		{Type: "bucket", Name: "docs", AppliedRevision: 3, Status: "failed"},
	}}, 4)
	require.Error(t, err, "a failed target fails the wait whatever the others did")
	assert.Contains(t, err.Error(), "app api: the edge refused the policy: too many sources")
	assert.Contains(t, err.Error(), "website docs: the edge reported a failure with no message")
	assert.NotContains(t, err.Error(), "app web")
}

// fastFirewallWait shrinks the poll and the bound for a test.
func fastFirewallWait(t *testing.T, bound time.Duration) {
	t.Helper()
	poll, wait := firewallPoll, firewallWait
	firewallPoll, firewallWait = time.Millisecond, bound
	t.Cleanup(func() { firewallPoll, firewallWait = poll, wait })
}

// firewallReads serves GET /api/v1/firewalls/fw-1 with each of states in
// turn, repeating the last.
func firewallReads(t *testing.T, states ...client.Firewall) (*client.Client, *int) {
	t.Helper()
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/firewalls/fw-1" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		i := min(reads, len(states)-1)
		reads++
		_ = json.NewEncoder(w).Encode(states[i])
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, "fp-test"), &reads
}

func firewallWith(targets ...client.FirewallTarget) client.Firewall {
	return client.Firewall{ID: "fw-1", Name: "office", Revision: 4, Targets: targets}
}

func TestWaitForFirewall_FollowsEveryTargetToTheRevision(t *testing.T) {
	fastFirewallWait(t, time.Minute)
	c, reads := firewallReads(t,
		firewallWith(client.FirewallTarget{Type: "app", Name: "web", AppliedRevision: 3, Status: "pending"}, client.FirewallTarget{Type: "bucket", Name: "docs", AppliedRevision: 3, Status: "pending"}),
		firewallWith(client.FirewallTarget{Type: "app", Name: "web", AppliedRevision: 4, Status: "applied"}, client.FirewallTarget{Type: "bucket", Name: "docs", AppliedRevision: 3, Status: "pending"}),
		firewallWith(client.FirewallTarget{Type: "app", Name: "web", AppliedRevision: 4, Status: "applied"}, client.FirewallTarget{Type: "bucket", Name: "docs", AppliedRevision: 4, Status: "applied"}),
	)
	fw, err := waitForFirewall(t.Context(), c, "fw-1", 4)
	require.NoError(t, err)
	assert.Equal(t, 3, *reads)
	assert.Equal(t, "applied", fw.Targets[1].Status)
}

func TestWaitForFirewall_AFailedTargetEndsTheWaitWithItsMessage(t *testing.T) {
	fastFirewallWait(t, time.Minute)
	c, reads := firewallReads(t,
		firewallWith(client.FirewallTarget{Type: "app", Name: "web", AppliedRevision: 3, Status: "pending"}),
		firewallWith(client.FirewallTarget{Type: "app", Name: "web", AppliedRevision: 3, Status: "failed", Message: "the route was not accepted"}),
	)
	_, err := waitForFirewall(t.Context(), c, "fw-1", 4)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the route was not accepted")
	assert.Contains(t, err.Error(), "revision 4")
	assert.Equal(t, 2, *reads)
}

func TestWaitForFirewall_IsBounded(t *testing.T) {
	fastFirewallWait(t, 20*time.Millisecond)
	c, _ := firewallReads(t, firewallWith(
		client.FirewallTarget{Type: "app", Name: "web", AppliedRevision: 4, Status: "applied"},
		client.FirewallTarget{Type: "bucket", Name: "docs", AppliedRevision: 3, Status: "pending"},
	))
	_, err := waitForFirewall(t.Context(), c, "fw-1", 4)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.Contains(t, err.Error(), "website docs", "the timeout names what is behind")
	assert.NotContains(t, err.Error(), "app web")
}

func TestWaitForFirewall_NothingAttachedReturnsOnTheFirstRead(t *testing.T) {
	fastFirewallWait(t, time.Minute)
	c, reads := firewallReads(t, firewallWith())
	_, err := waitForFirewall(t.Context(), c, "fw-1", 4)
	require.NoError(t, err)
	assert.Equal(t, 1, *reads)
}

// refs returns a read that yields each ref in turn, repeating the last.
func refs(states ...*client.FirewallRef) func() (*client.FirewallRef, error) {
	i := 0
	return func() (*client.FirewallRef, error) {
		ref := states[min(i, len(states)-1)]
		i++
		return ref, nil
	}
}

func TestWaitForFirewallOn(t *testing.T) {
	fastFirewallWait(t, 20*time.Millisecond)

	// The firewall the target had is still the one it reports until the attach
	// lands, and its being applied says nothing about the new one.
	ref, err := waitForFirewallOn("the app", refs(
		&client.FirewallRef{ID: "fw-old", Name: "old", Revision: 9, AppliedRevision: 9, Status: "applied"},
		&client.FirewallRef{ID: "fw-1", Name: "office", Revision: 4, AppliedRevision: 0, Status: "pending"},
		&client.FirewallRef{ID: "fw-1", Name: "office", Revision: 4, AppliedRevision: 4, Status: "applied"},
	), "fw-1", 4)
	require.NoError(t, err)
	assert.Equal(t, 4, ref.AppliedRevision)

	_, err = waitForFirewallOn("the app", refs(
		&client.FirewallRef{ID: "fw-1", Name: "office", Revision: 4, Status: "failed", Message: "the route was not accepted"},
	), "fw-1", 4)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the route was not accepted")

	_, err = waitForFirewallOn("the app", refs(nil), "fw-1", 4)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestWaitForFirewallOff(t *testing.T) {
	fastFirewallWait(t, 20*time.Millisecond)

	require.NoError(t, waitForFirewallOff("the app", refs(
		&client.FirewallRef{Status: "pending"},
		&client.FirewallRef{Status: "pending"},
		nil,
	)))

	err := waitForFirewallOff("the app", refs(&client.FirewallRef{Status: "failed", Message: "the policy could not be removed"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the policy could not be removed")

	err = waitForFirewallOff("the app", refs(&client.FirewallRef{Status: "pending"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

// A refusal the API names by code says what to do about it, and stays the
// API's error underneath so the exit status is unchanged.
func TestExplainFirewallError(t *testing.T) {
	notReady := &client.APIError{StatusCode: http.StatusServiceUnavailable, Code: "FIREWALL_EDGE_NOT_READY", Message: "the edge is not ready"}
	err := explainFirewallError(notReady)
	assert.Contains(t, err.Error(), "the edge is not ready")
	assert.Contains(t, err.Error(), "again")
	var apiErr *client.APIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, exitUnavailable, exitCode(err), "a transient refusal keeps the status a caller retries on")

	other := &client.APIError{StatusCode: http.StatusBadRequest, Code: "VALIDATION", Message: "bad"}
	assert.Same(t, error(other), explainFirewallError(other))
	plain := errors.New("connection reset")
	assert.Same(t, plain, explainFirewallError(plain))
}

func TestFirewallDetachHint(t *testing.T) {
	hint := firewallDetachHint(&client.Firewall{Name: "office", Targets: []client.FirewallTarget{
		{Type: "app", Name: "web"}, {Type: "bucket", Name: "docs"},
	}})
	assert.Contains(t, hint, "fpcloud app update web --no-firewall")
	assert.Contains(t, hint, "fpcloud website update docs --no-firewall")
}

func TestFirewallDecisionLine(t *testing.T) {
	assert.Equal(t, "203.0.113.7: allow, by default (no rule matches)",
		stripANSI(firewallDecisionLine(&client.FirewallDecision{Address: "203.0.113.7", Action: "allow"})))
	assert.Equal(t, "203.0.113.7: deny, by rule 200 (scanner)",
		stripANSI(firewallDecisionLine(&client.FirewallDecision{Address: "203.0.113.7", Action: "deny", Rule: &client.FirewallRule{Priority: 200, Description: "scanner"}})))
	assert.Equal(t, "203.0.113.7: deny, by rule 200",
		stripANSI(firewallDecisionLine(&client.FirewallDecision{Address: "203.0.113.7", Action: "deny", Rule: &client.FirewallRule{Priority: 200}})))
}

func TestFirewallRefDisplay(t *testing.T) {
	assert.Equal(t, "none", stripANSI(firewallRefDisplay(nil)))
	assert.Contains(t, stripANSI(firewallRefDisplay(&client.FirewallRef{Status: "pending"})), "being detached")
	assert.Equal(t, "office (revision 4) ● applied",
		stripANSI(firewallRefDisplay(&client.FirewallRef{ID: "fw-1", Name: "office", Revision: 4, AppliedRevision: 4, Status: "applied"})))
	behind := stripANSI(firewallRefDisplay(&client.FirewallRef{ID: "fw-1", Name: "office", Revision: 4, AppliedRevision: 3, Status: "failed", Message: "not accepted"}))
	assert.Contains(t, behind, "revision 4, the edge enforces 3")
	assert.Contains(t, behind, "not accepted")
}

// Every command that changes what the edge enforces waits for it unless told
// not to.
func TestEveryEdgeChangingFirewallCommandWaits(t *testing.T) {
	for _, c := range []*cobra.Command{
		firewallUpdateCmd, firewallRollbackCmd,
		firewallRuleAddCmd, firewallRuleRemoveCmd, firewallRuleEnableCmd, firewallRuleDisableCmd,
	} {
		noWait := c.Flags().Lookup("no-wait")
		require.NotNil(t, noWait, c.CommandPath())
		assert.Equal(t, "false", noWait.DefValue, c.CommandPath())
		assert.Nil(t, c.Flags().Lookup("wait"), c.CommandPath())
	}
}

// The group is on the root with every verb, and the dense help an agent reads
// carries it, flags included.
func TestFirewallCommandTree(t *testing.T) {
	group, _, err := rootCmd.Find([]string{"firewall"})
	require.NoError(t, err)
	require.Same(t, firewallCmd, group)

	var paths []string
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Runnable() {
				paths = append(paths, sub.CommandPath())
			}
			walk(sub)
		}
	}
	walk(firewallCmd)
	assert.ElementsMatch(t, []string{
		"fpcloud firewall create",
		"fpcloud firewall list",
		"fpcloud firewall get",
		"fpcloud firewall update",
		"fpcloud firewall delete",
		"fpcloud firewall revisions",
		"fpcloud firewall rollback",
		"fpcloud firewall test",
		"fpcloud firewall rule add",
		"fpcloud firewall rule remove",
		"fpcloud firewall rule enable",
		"fpcloud firewall rule disable",
	}, paths)

	for alias, want := range map[string]*cobra.Command{"ls": firewallListCmd, "rm": firewallDeleteCmd} {
		got, _, err := rootCmd.Find([]string{"firewall", alias})
		require.NoError(t, err)
		assert.Same(t, want, got, alias)
	}

	help := renderLLMHelp(rootCmd)
	for _, want := range []string{
		"fpcloud firewall create", "fpcloud firewall rule add", "fpcloud firewall rollback", "fpcloud firewall test",
		"--default-action", "--rule", "--no-wait",
	} {
		assert.Contains(t, help, want)
	}
}

// Help is read in a terminal and by agents as plain text: no markdown code
// marks in it, and the rule and file forms are where a reader looks first.
func TestFirewallHelpIsPlainAndDocumentsBothForms(t *testing.T) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, text := range []string{c.Short, c.Long} {
			assert.NotContains(t, text, "`", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(firewallCmd)

	assert.Contains(t, firewallCreateCmd.Long, `--rule "<priority> <allow|deny> <cidr>[,<cidr>...]"`)
	for _, c := range []*cobra.Command{firewallCreateCmd, firewallUpdateCmd} {
		assert.Contains(t, c.Long, "default_action: deny", c.CommandPath())
		assert.Contains(t, c.Long, "sources: [", c.CommandPath())
	}

	// The example in the help is itself a valid file.
	start := strings.Index(firewallFileHelp, "  description:")
	end := strings.Index(firewallFileHelp, "\nA rule's enabled")
	require.True(t, start >= 0 && end > start)
	file, err := readFirewallFile("-", strings.NewReader(firewallFileHelp[start:end]))
	require.NoError(t, err)
	assert.Len(t, file.Rules, 2)
}

// fakeFirewallAPI is the firewall routes over one firewall, fw-1 "office" in
// project p1, attached to app web. The edge follows a change one read later:
// the read after a write still reports the old applied revision.
type fakeFirewallAPI struct {
	mu       sync.Mutex
	fw       client.Firewall
	updates  []client.UpdateFirewallRequest
	rollback []client.RollbackFirewallRequest
	attached []client.AttachFirewallRequest
	detached int
	appRef   *client.FirewallRef
	lag      int
	refuse   *client.APIError
	// racing is how many of the next conditional updates find that somebody
	// else's change, a rule at a priority from 900 up, was stored first.
	racing int
	// findings is what the next write or attach answers with.
	findings []client.FirewallFinding
}

func newFakeFirewallAPI(t *testing.T) *fakeFirewallAPI {
	t.Helper()
	f := &fakeFirewallAPI{fw: client.Firewall{
		ID: "fw-1", ProjectID: "p1", Name: "office", Description: "office only", Revision: 3, DefaultAction: "deny",
		Rules: []client.FirewallRule{
			{Priority: 100, Action: "allow", Description: "office", Enabled: true, Match: client.FirewallMatch{Sources: []string{"198.51.100.0/24"}}},
			{Priority: 200, Action: "deny", Enabled: false, Match: client.FirewallMatch{Sources: []string{"203.0.113.7"}}},
		},
		Targets: []client.FirewallTarget{{Type: "app", ID: "app-1", Name: "web", AppliedRevision: 3, Status: "applied"}},
	}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FPCLOUD_API_URL", srv.URL)
	t.Setenv("FPCLOUD_API_KEY", "test-key")
	return f
}

func (f *fakeFirewallAPI) store(description, action string, rules []client.FirewallRuleRequest) {
	f.fw.Description, f.fw.DefaultAction = description, action
	f.fw.Rules = nil
	for _, r := range rules {
		f.fw.Rules = append(f.fw.Rules, client.FirewallRule{
			Priority: r.Priority, Action: r.Action, Description: r.Description,
			Enabled: r.Enabled == nil || *r.Enabled, Match: r.Match,
		})
	}
	f.fw.Revision++
	f.fw.Targets[0].Status = "pending"
	f.lag = 1
}

func (f *fakeFirewallAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writes := r.Method != http.MethodGet
	if writes && f.refuse != nil {
		w.WriteHeader(f.refuse.StatusCode)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": f.refuse.Code, "message": f.refuse.Message}})
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /api/v1/projects/p1/firewalls":
		_ = json.NewEncoder(w).Encode([]client.Firewall{f.fw})
	case "GET /api/v1/firewalls/fw-1":
		if f.lag > 0 {
			f.lag--
		} else {
			f.fw.Targets[0].AppliedRevision, f.fw.Targets[0].Status = f.fw.Revision, "applied"
		}
		_ = json.NewEncoder(w).Encode(f.fw)
	case "PUT /api/v1/firewalls/fw-1":
		var req client.UpdateFirewallRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.updates = append(f.updates, req)
		if req.IfRevision != 0 && f.racing > 0 {
			f.racing--
			f.fw.Rules = append(f.fw.Rules, client.FirewallRule{Priority: 900 + f.racing, Action: "deny", Enabled: true, Match: client.FirewallMatch{Sources: []string{"192.0.2.9/32"}}})
			f.fw.Revision++
		}
		if req.IfRevision != 0 && req.IfRevision != f.fw.Revision {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "FIREWALL_REVISION_CONFLICT",
				"message": fmt.Sprintf("firewall \"office\" was changed since it was read: it is at revision %d, and this change was made to revision %d", f.fw.Revision, req.IfRevision)}})
			return
		}
		f.store(req.Description, req.DefaultAction, req.Rules)
		answer := f.fw
		answer.Findings, f.findings = f.findings, nil
		_ = json.NewEncoder(w).Encode(answer)
	case "POST /api/v1/firewalls/fw-1/rollback":
		var req client.RollbackFirewallRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.rollback = append(f.rollback, req)
		f.store(f.fw.Description, "allow", nil)
		_ = json.NewEncoder(w).Encode(f.fw)
	case "GET /api/v1/projects/p1/apps":
		_ = json.NewEncoder(w).Encode([]client.App{{ID: "app-1", Name: "web"}})
	case "PUT /api/v1/apps/app-1/firewall":
		var req client.AttachFirewallRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.attached = append(f.attached, req)
		f.appRef = &client.FirewallRef{ID: "fw-1", Name: "office", Revision: f.fw.Revision, Status: "pending"}
		f.lag = 1
		answer := *f.appRef
		answer.Findings, f.findings = f.findings, nil
		_ = json.NewEncoder(w).Encode(answer)
	case "DELETE /api/v1/apps/app-1/firewall":
		f.detached++
		f.appRef = &client.FirewallRef{Status: "pending"}
		f.lag = 1
		w.WriteHeader(http.StatusNoContent)
	case "GET /api/v1/apps/app-1":
		if f.lag > 0 {
			f.lag--
		} else if f.appRef != nil && f.appRef.ID == "" {
			f.appRef = nil
		} else if f.appRef != nil {
			f.appRef.AppliedRevision, f.appRef.Status = f.appRef.Revision, "applied"
		}
		_ = json.NewEncoder(w).Encode(client.App{ID: "app-1", Name: "web", Firewall: f.appRef})
	default:
		http.Error(w, `{"error":{"code":"NOT_FOUND","message":"no route `+r.Method+" "+r.URL.Path+`"}}`, http.StatusNotFound)
	}
}

// runFpcloud runs the real command tree in project p1 with JSON output, then
// returns every flag the run set to its default so the next run starts clean.
func runFpcloud(t *testing.T, args ...string) error {
	t.Helper()
	rootCmd.SetArgs(append(args, "--project", "p1", "-o", "json"))
	defer func() {
		rootCmd.SetArgs(nil)
		reset := func(f *pflag.Flag) {
			if !f.Changed {
				return
			}
			if slice, ok := f.Value.(pflag.SliceValue); ok {
				_ = slice.Replace(nil)
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		}
		var walk func(*cobra.Command)
		walk = func(c *cobra.Command) {
			c.Flags().VisitAll(reset)
			c.PersistentFlags().VisitAll(reset)
			for _, sub := range c.Commands() {
				walk(sub)
			}
		}
		walk(rootCmd)
	}()
	return rootCmd.Execute()
}

func TestFirewallRuleCommandsRewriteTheWholeListAndWait(t *testing.T) {
	fastFirewallWait(t, time.Minute)
	f := newFakeFirewallAPI(t)

	require.NoError(t, runFpcloud(t, "firewall", "rule", "add", "office",
		"--priority", "150", "--action", "allow", "--source", "2001:db8::/32,203.0.113.9", "--source", "192.0.2.0/24", "--description", "vpn"))
	require.Len(t, f.updates, 1)
	sent := f.updates[0]
	assert.Equal(t, "office only", sent.Description, "what the command does not name is sent as it was read")
	assert.Equal(t, "deny", sent.DefaultAction)
	require.Len(t, sent.Rules, 3)
	assert.False(t, *sent.Rules[1].Enabled, "the disabled rule stays disabled")
	assert.Equal(t, client.FirewallMatch{Sources: []string{"2001:db8::/32", "203.0.113.9", "192.0.2.0/24"}}, sent.Rules[2].Match)
	assert.Equal(t, "vpn", sent.Rules[2].Description)
	assert.True(t, *sent.Rules[2].Enabled)
	assert.Equal(t, 4, f.fw.Targets[0].AppliedRevision, "the command returned only once the target enforced the new revision")

	err := runFpcloud(t, "firewall", "rule", "add", "office", "--priority", "150", "--action", "deny", "--source", "10.0.0.0/8")
	require.Error(t, err, "a priority in use is refused, since writing it would replace that rule")
	assert.Contains(t, err.Error(), "already has a rule at priority 150")
	assert.Len(t, f.updates, 1)

	require.NoError(t, runFpcloud(t, "firewall", "rule", "enable", "office", "200"))
	require.Len(t, f.updates, 2)
	assert.True(t, *f.updates[1].Rules[1].Enabled)
	assert.True(t, *f.updates[1].Rules[0].Enabled)

	require.NoError(t, runFpcloud(t, "firewall", "rule", "enable", "office", "200"))
	assert.Len(t, f.updates, 2, "a rule already enabled stores no revision")

	require.NoError(t, runFpcloud(t, "firewall", "rule", "disable", "office", "100"))
	require.Len(t, f.updates, 3)
	assert.False(t, *f.updates[2].Rules[0].Enabled)
	assert.Equal(t, []int{3, 4, 5}, []int{f.updates[0].IfRevision, f.updates[1].IfRevision, f.updates[2].IfRevision},
		"each write is on condition of the revision it read")

	require.NoError(t, runFpcloud(t, "firewall", "rule", "remove", "office", "150"))
	require.Len(t, f.updates, 4)
	require.Len(t, f.updates[3].Rules, 2)
	assert.Equal(t, []int{100, 200}, []int{f.updates[3].Rules[0].Priority, f.updates[3].Rules[1].Priority})

	err = runFpcloud(t, "firewall", "rule", "remove", "office", "999")
	require.Error(t, err)
	assert.Equal(t, exitNotFound, exitCode(err))

	err = runFpcloud(t, "firewall", "rule", "add", "office", "--priority", "5", "--action", "drop", "--source", "10.0.0.0/8")
	require.Error(t, err)
	assert.True(t, isUsageErrorValue(err))
	assert.Len(t, f.updates, 4)
}

func TestFirewallUpdate_FlagsKeepTheRulesAndAFileReplacesThem(t *testing.T) {
	fastFirewallWait(t, time.Minute)
	f := newFakeFirewallAPI(t)

	require.NoError(t, runFpcloud(t, "firewall", "update", "office", "--default-action", "allow"))
	require.Len(t, f.updates, 1)
	assert.Equal(t, "allow", f.updates[0].DefaultAction)
	assert.Equal(t, "office only", f.updates[0].Description)
	assert.Len(t, f.updates[0].Rules, 2, "changing the default action keeps the rules")

	path := filepath.Join(t.TempDir(), "office.yaml")
	require.NoError(t, os.WriteFile(path, []byte("default_action: deny\nrules:\n  - priority: 10\n    action: allow\n    match:\n      sources: [192.0.2.0/24]\n"), 0o600))
	require.NoError(t, runFpcloud(t, "firewall", "update", "office", "-f", path, "--description", "from a file"))
	require.Len(t, f.updates, 2)
	assert.Equal(t, "deny", f.updates[1].DefaultAction)
	assert.Equal(t, "from a file", f.updates[1].Description)
	require.Len(t, f.updates[1].Rules, 1, "a file replaces the rules in full")
	assert.Equal(t, 10, f.updates[1].Rules[0].Priority)

	err := runFpcloud(t, "firewall", "update", "office")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to update")

	// --no-wait returns with the target still behind.
	require.NoError(t, runFpcloud(t, "firewall", "update", "office", "--default-action", "allow", "--no-wait"))
	assert.Equal(t, "pending", f.fw.Targets[0].Status)

	assert.Equal(t, []int{3, 0, 5}, []int{f.updates[0].IfRevision, f.updates[1].IfRevision, f.updates[2].IfRevision},
		"a flag changes one thing on the firewall as it was read; a file is the whole state and is written on no condition")
}

// A rule change that loses to somebody else's is made again on the rules as
// they now are, so both changes are in what is stored.
func TestARuleChangeIsMadeAgainOnTheRulesItLostTo(t *testing.T) {
	fastFirewallWait(t, time.Minute)
	f := newFakeFirewallAPI(t)

	f.racing = 2
	require.NoError(t, runFpcloud(t, "firewall", "rule", "add", "office", "--priority", "150", "--action", "allow", "--source", "192.0.2.0/24"))
	require.Len(t, f.updates, 3)
	assert.Equal(t, []int{3, 4, 5}, []int{f.updates[0].IfRevision, f.updates[1].IfRevision, f.updates[2].IfRevision})
	var stored []int
	for _, r := range f.fw.Rules {
		stored = append(stored, r.Priority)
	}
	assert.ElementsMatch(t, []int{100, 200, 901, 900, 150}, stored, "the two changes it lost to are kept beside its own")

	// With nobody else writing, each command writes once.
	f.racing = 0
	f.updates = nil
	require.NoError(t, runFpcloud(t, "firewall", "rule", "disable", "office", "900"))
	require.NoError(t, runFpcloud(t, "firewall", "update", "office", "--description", "busy"))
	assert.Len(t, f.updates, 2)

	// It gives up after three more attempts, with the API's message and what
	// to do.
	f.racing = 10
	f.updates = nil
	before := f.fw.Description
	err := runFpcloud(t, "firewall", "update", "office", "--description", "never lands")
	require.Error(t, err)
	assert.Len(t, f.updates, 4, "the first attempt and three more")
	assert.Contains(t, err.Error(), "was changed since it was read")
	assert.Contains(t, err.Error(), "kept changing while this command ran")
	assert.Contains(t, err.Error(), "run the command again")
	assert.Equal(t, before, f.fw.Description)
	var apiErr *client.APIError
	require.True(t, errors.As(err, &apiErr), "the exit status stays the API's")
}

// A write that denies the caller is made, and its warning reaches standard
// error with the output still on standard output.
func TestAChangeThatDeniesTheCallerIsMadeAndWarned(t *testing.T) {
	fastFirewallWait(t, time.Minute)
	f := newFakeFirewallAPI(t)
	warning := client.FirewallFinding{Code: "denies_caller", Message: "203.0.113.7, the address you are connecting from, is denied by this firewall as it is now stored (by its default action)."}

	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = wOut, wErr
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()

	f.findings = []client.FirewallFinding{warning}
	errUpdate := runFpcloud(t, "firewall", "update", "office", "--default-action", "allow")
	f.findings = []client.FirewallFinding{warning}
	errAttach := runFpcloud(t, "app", "update", "web", "--firewall", "office")
	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout, os.Stderr = stdout, stderr
	out, _ := io.ReadAll(rOut)
	errOut, _ := io.ReadAll(rErr)

	require.NoError(t, errUpdate)
	require.NoError(t, errAttach)
	assert.Equal(t, "allow", f.fw.DefaultAction, "the change was stored")
	assert.Contains(t, string(out), `"code": "denies_caller"`, "structured output carries the update's finding")
	assert.Equal(t, 1, strings.Count(string(errOut), "the address you are connecting from"),
		"the attach's warning is printed, since the app read that follows does not carry it: %s", errOut)
}

func TestFirewallRollbackWaitsForTheNewRevision(t *testing.T) {
	fastFirewallWait(t, time.Minute)
	f := newFakeFirewallAPI(t)

	require.NoError(t, runFpcloud(t, "firewall", "rollback", "office", "2"))
	assert.Equal(t, []client.RollbackFirewallRequest{{Revision: 2}}, f.rollback)
	assert.Equal(t, 4, f.fw.Targets[0].AppliedRevision)
	assert.Equal(t, "applied", f.fw.Targets[0].Status)

	err := runFpcloud(t, "firewall", "rollback", "office", "latest")
	require.Error(t, err)
	assert.True(t, isUsageErrorValue(err))
	assert.Len(t, f.rollback, 1)
}
