package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

var firewallCmd = &cobra.Command{
	Use:     "firewall",
	Aliases: []string{"firewalls"},
	Short:   "Manage firewalls: ordered allow and deny rules on client addresses, attached to apps and websites",
	Long: `A firewall is a project's ordered access list for what the edge serves. Each
rule allows or denies a set of client address ranges; rules are evaluated in
priority order, lowest first, the first one that matches decides, and a
request no rule matches gets the default action. A firewall does nothing until
it is attached to an app or a website, and then covers every host served for
that target. It covers requests arriving from outside and nothing else: not
calls between apps, and not outbound traffic.

  fpcloud firewall create office --default-action deny \
      --rule "100 allow 198.51.100.0/24,2001:db8::/32"
  fpcloud app update web --firewall office
  fpcloud website update docs --firewall office
  fpcloud firewall get office

Rules are changed one at a time, or replaced in full from a file:

  fpcloud firewall rule add office --priority 200 --action allow --source 203.0.113.7
  fpcloud firewall rule disable office 200
  fpcloud firewall rule remove office 200
  fpcloud firewall update office -f office.yaml

Every change to the rules or the default action stores a new revision, and the
edge follows it. A command that changes what the edge enforces waits until
every app and website the firewall is attached to enforces the new revision,
and fails if one of them could not take it; --no-wait returns at once.

  fpcloud firewall test office 203.0.113.7
  fpcloud firewall revisions office
  fpcloud firewall rollback office 3

A change that would deny the address you are calling from is refused while
the firewall is attached; --allow-lockout makes it anyway.

Detach a firewall with --no-firewall on the same two commands:

  fpcloud app update web --no-firewall
  fpcloud website update docs --no-firewall`,
}

// firewallFileHelp documents the -f file once, for create and update.
const firewallFileHelp = `The file is YAML or JSON, and - reads it from standard input:

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

A rule's enabled left out is true. The output of firewall get -o yaml is
accepted as it is, so a firewall can be saved, edited and written back.`

// firewallRuleHelp documents the --rule shorthand.
const firewallRuleHelp = `--rule takes one rule as three space-separated fields, the last a
comma-separated list of address ranges with no spaces in it:

  --rule "<priority> <allow|deny> <cidr>[,<cidr>...]"
  --rule "100 allow 198.51.100.0/24,2001:db8::/32"

A bare address is the range holding only it. A rule written this way is
enabled and has no description; use a file or firewall rule add for those.`

var firewallCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a firewall, with its rules from --rule or a file",
	Long: `Create a firewall in the project. It is attached to nothing yet, so it
changes nothing until an app or a website is given it.

--default-action left out is allow, so a firewall with no rules lets every
request through.

  fpcloud firewall create office --default-action deny \
      --rule "100 allow 198.51.100.0/24,2001:db8::/32" \
      --rule "200 allow 203.0.113.7"
  fpcloud firewall create office -f office.yaml

` + firewallRuleHelp + `

` + firewallFileHelp + `

--rule and -f cannot be combined. --description and --default-action win over
what the file says.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		req := client.CreateFirewallRequest{Name: args[0]}
		if path, _ := cmd.Flags().GetString("file"); path != "" {
			file, err := readFirewallFile(path, cmd.InOrStdin())
			if err != nil {
				return err
			}
			if file.Description != nil {
				req.Description = *file.Description
			}
			req.DefaultAction = file.DefaultAction
			req.Rules = file.Rules
		}
		specs, _ := cmd.Flags().GetStringArray("rule")
		rules, err := parseFirewallRuleFlags(specs)
		if err != nil {
			return err
		}
		if len(rules) > 0 {
			req.Rules = rules
		}
		if cmd.Flags().Changed("description") {
			req.Description, _ = cmd.Flags().GetString("description")
		}
		if cmd.Flags().Changed("default-action") {
			action, _ := cmd.Flags().GetString("default-action")
			if err := checkFirewallAction("--default-action", action); err != nil {
				return err
			}
			req.DefaultAction = action
		}

		projectID, err := requireProject()
		if err != nil {
			return err
		}
		fw, err := getClient().CreateFirewall(cmd.Context(), projectID, req)
		if err != nil {
			return explainFirewallError(err)
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(fw)
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Firewall %q created. Attach it with `fpcloud app update <app> --firewall %s`", fw.Name, fw.Name),
		))
		printFirewallFindings(fw.Findings)
		return nil
	},
}

var firewallListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List the project's firewalls",
	Aliases: []string{"ls"},
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID, err := requireProject()
		if err != nil {
			return err
		}
		firewalls, err := getClient().ListFirewalls(cmd.Context(), projectID)
		if err != nil {
			return err
		}
		rows := make([][]string, len(firewalls))
		for i, fw := range firewalls {
			rows[i] = []string{
				fw.Name,
				firewallAction(fw.DefaultAction),
				strconv.Itoa(len(fw.Rules)),
				strconv.Itoa(fw.Revision),
				firewallTargetNames(fw.Targets),
			}
		}
		render([]string{"NAME", "DEFAULT", "RULES", "REVISION", "ATTACHED TO"}, rows, firewalls)
		return nil
	},
}

var firewallGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show a firewall: its rules in priority order, what it is attached to and what the edge enforces there",
	Long: `Show a firewall: the default action, the rules in the order they are
evaluated, and every app and website it is attached to with the revision the
edge enforces there. A target is up to date when its applied revision equals
the firewall's and its status is applied.

Warnings about the firewall as written, such as a rule no request can reach,
are printed after it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		found, err := resolveFirewall(cmd.Context(), c, args[0])
		if err != nil {
			return err
		}
		fw, err := c.GetFirewall(cmd.Context(), found.ID)
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(fw)
		}
		printFirewall(fw)
		return nil
	},
}

var firewallUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Change a firewall's description or default action, or replace its rules from a file",
	Long: `Change a firewall. --description and --default-action change that one thing
and keep the rules. -f replaces the rules in full with the file's, and takes
the description and the default action from it where it states them; the two
flags win over the file.

  fpcloud firewall update office --default-action deny
  fpcloud firewall update office -f office.yaml

` + firewallFileHelp + `

To change one rule, use firewall rule add, remove, enable or disable.

The command waits until every app and website the firewall is attached to
enforces the change; --no-wait returns once it is stored. A change that would
deny the address you are calling from is refused while the firewall is
attached, unless --allow-lockout is given.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, _ := cmd.Flags().GetString("file")
		if path == "" && !cmd.Flags().Changed("description") && !cmd.Flags().Changed("default-action") {
			return fmt.Errorf("nothing to update: pass --description, --default-action and/or -f")
		}
		var file *firewallFile
		if path != "" {
			var err error
			if file, err = readFirewallFile(path, cmd.InOrStdin()); err != nil {
				return err
			}
		}
		var action string
		if cmd.Flags().Changed("default-action") {
			action, _ = cmd.Flags().GetString("default-action")
			if err := checkFirewallAction("--default-action", action); err != nil {
				return err
			}
		}

		c := getClient()
		fw, err := resolveFirewall(cmd.Context(), c, args[0])
		if err != nil {
			return err
		}
		req := firewallUpdateOf(fw)
		if file != nil {
			if file.Description != nil {
				req.Description = *file.Description
			}
			if file.DefaultAction != "" {
				req.DefaultAction = file.DefaultAction
			}
			req.Rules = file.Rules
		}
		if cmd.Flags().Changed("description") {
			req.Description, _ = cmd.Flags().GetString("description")
		}
		if action != "" {
			req.DefaultAction = action
		}
		return applyFirewallUpdate(cmd, c, fw, req, "updated")
	},
}

var firewallDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Short:   "Delete a firewall nothing is attached to",
	Aliases: []string{"rm"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		fw, err := resolveFirewall(cmd.Context(), c, args[0])
		if err != nil {
			return err
		}
		if err := c.DeleteFirewall(cmd.Context(), fw.ID); err != nil {
			var apiErr *client.APIError
			if errors.As(err, &apiErr) && apiErr.Code == "FIREWALL_ATTACHED" {
				return fmt.Errorf("%w\n%s", err, firewallDetachHint(fw))
			}
			return explainFirewallError(err)
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(map[string]string{"status": "deleted", "name": fw.Name})
		}
		fmt.Println(successBox.Render(
			lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
				fmt.Sprintf(" Firewall %q deleted", fw.Name),
		))
		return nil
	},
}

var firewallRevisionsCmd = &cobra.Command{
	Use:   "revisions <name>",
	Short: "List a firewall's stored revisions, newest first",
	Long: `List a firewall's stored revisions, newest first. Each is the default action
and the rules as they stood; -o json or -o yaml prints the rules themselves.
firewall rollback re-applies one.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		fw, err := resolveFirewall(cmd.Context(), c, args[0])
		if err != nil {
			return err
		}
		revisions, err := c.ListFirewallRevisions(cmd.Context(), fw.ID)
		if err != nil {
			return err
		}
		rows := make([][]string, len(revisions))
		for i, rev := range revisions {
			number := strconv.Itoa(rev.Revision)
			if rev.Revision == fw.Revision {
				number += " (current)"
			}
			rows[i] = []string{
				number,
				firewallAction(rev.DefaultAction),
				strconv.Itoa(len(rev.Rules)),
				rev.CreatedAt.Format("2006-01-02 15:04"),
				dashIfBlank(rev.CreatedBy),
			}
		}
		render([]string{"REVISION", "DEFAULT", "RULES", "CREATED", "BY"}, rows, revisions)
		return nil
	},
}

var firewallRollbackCmd = &cobra.Command{
	Use:   "rollback <name> <revision>",
	Short: "Re-apply an earlier revision's rules and default action as a new revision",
	Long: `Re-apply an earlier revision. Its default action and rules are stored as a
new revision, so the history only grows and a rollback can itself be rolled
back. firewall revisions lists what there is to return to.

  fpcloud firewall rollback office 3

The command waits until every app and website the firewall is attached to
enforces the new revision; --no-wait returns once it is stored. A rollback
that would deny the address you are calling from is refused while the
firewall is attached, unless --allow-lockout is given.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		revision, err := strconv.Atoi(args[1])
		if err != nil || revision < 1 {
			return usageError{fmt.Errorf("revision %q is not a revision number; fpcloud firewall revisions %s lists them", args[1], args[0])}
		}
		c := getClient()
		fw, err := resolveFirewall(cmd.Context(), c, args[0])
		if err != nil {
			return err
		}
		allowLockout, _ := cmd.Flags().GetBool("allow-lockout")
		updated, err := c.RollbackFirewall(cmd.Context(), fw.ID, client.RollbackFirewallRequest{Revision: revision, AllowLockout: allowLockout})
		if err != nil {
			return explainFirewallError(err)
		}
		return finishFirewallChange(cmd, c, updated, fmt.Sprintf("rolled back to revision %d", revision))
	},
}

var firewallTestCmd = &cobra.Command{
	Use:   "test <name> <address>",
	Short: "Say what a firewall does with a request from an address, and which rule decides",
	Long: `Evaluate the firewall's current revision against one client address and
print the action and the rule that decided, or that the default action did.
Nothing is sent to the app or the website; this reads the rules only, so it
works on a firewall attached to nothing.

  fpcloud firewall test office 203.0.113.7`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := getClient()
		fw, err := resolveFirewall(cmd.Context(), c, args[0])
		if err != nil {
			return err
		}
		decision, err := c.TestFirewall(cmd.Context(), fw.ID, client.TestFirewallRequest{Address: args[1]})
		if err != nil {
			return err
		}
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(decision)
		}
		fmt.Println(firewallDecisionLine(decision))
		return nil
	},
}

var firewallRuleCmd = &cobra.Command{
	Use:     "rule",
	Aliases: []string{"rules"},
	Short:   "Add, remove, enable or disable one rule of a firewall",
	Long: `Change one rule of a firewall. A rule is named by its priority, which is
unique within the firewall and is also its place in the order: lower is
evaluated first.

  fpcloud firewall rule add office --priority 100 --action allow --source 198.51.100.0/24
  fpcloud firewall rule disable office 100
  fpcloud firewall rule enable office 100
  fpcloud firewall rule remove office 100

Each of these stores a new revision and waits until every app and website the
firewall is attached to enforces it; --no-wait returns once it is stored. Two
people changing rules of one firewall at the same moment can overwrite each
other, because a firewall's rules are written as a whole.`,
}

var firewallRuleAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a rule to a firewall",
	Long: `Add a rule. --source takes an address range as a CIDR, IPv4 or IPv6, and a
bare address for the range holding only it; repeat it or separate ranges with
commas. The priority must not be in use: remove the rule holding it first, or
pick another.

  fpcloud firewall rule add office --priority 100 --action allow \
      --source 198.51.100.0/24 --source 2001:db8::/32 --description office
  fpcloud firewall rule add office --priority 900 --action deny \
      --source 203.0.113.0/24 --disabled

--disabled stores the rule without evaluating it, until firewall rule enable.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		priority, _ := cmd.Flags().GetInt("priority")
		action, _ := cmd.Flags().GetString("action")
		sources, _ := cmd.Flags().GetStringSlice("source")
		description, _ := cmd.Flags().GetString("description")
		disabled, _ := cmd.Flags().GetBool("disabled")
		if err := checkFirewallAction("--action", action); err != nil {
			return err
		}
		if err := checkFirewallSources(sources); err != nil {
			return usageError{fmt.Errorf("--source: %w", err)}
		}
		enabled := !disabled
		rule := client.FirewallRuleRequest{
			Priority:    priority,
			Action:      action,
			Description: description,
			Enabled:     &enabled,
			Match:       client.FirewallMatch{Sources: sources},
		}
		return changeFirewallRules(cmd, args[0], fmt.Sprintf("rule %d added", priority), func(rules []client.FirewallRuleRequest) ([]client.FirewallRuleRequest, error) {
			if firewallRuleIndex(rules, priority) >= 0 {
				return nil, fmt.Errorf("firewall %q already has a rule at priority %d; remove it first or pick another priority", args[0], priority)
			}
			return append(rules, rule), nil
		})
	},
}

var firewallRuleRemoveCmd = &cobra.Command{
	Use:     "remove <name> <priority>",
	Short:   "Remove the rule at a priority",
	Aliases: []string{"rm"},
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		priority, err := firewallPriorityArg(args[1])
		if err != nil {
			return err
		}
		return changeFirewallRules(cmd, args[0], fmt.Sprintf("rule %d removed", priority), func(rules []client.FirewallRuleRequest) ([]client.FirewallRuleRequest, error) {
			i := firewallRuleIndex(rules, priority)
			if i < 0 {
				return nil, notFoundf("firewall %q has no rule at priority %d", args[0], priority)
			}
			return append(rules[:i:i], rules[i+1:]...), nil
		})
	},
}

var firewallRuleEnableCmd = &cobra.Command{
	Use:   "enable <name> <priority>",
	Short: "Evaluate a disabled rule again",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setFirewallRuleEnabled(cmd, args, true)
	},
}

var firewallRuleDisableCmd = &cobra.Command{
	Use:   "disable <name> <priority>",
	Short: "Keep a rule and stop evaluating it",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setFirewallRuleEnabled(cmd, args, false)
	},
}

// setFirewallRuleEnabled is rule enable and rule disable. A rule already in
// the state asked for is left alone, so no revision is stored for a change
// that changes nothing.
func setFirewallRuleEnabled(cmd *cobra.Command, args []string, enabled bool) error {
	priority, err := firewallPriorityArg(args[1])
	if err != nil {
		return err
	}
	verb := "disabled"
	if enabled {
		verb = "enabled"
	}
	return changeFirewallRules(cmd, args[0], fmt.Sprintf("rule %d %s", priority, verb), func(rules []client.FirewallRuleRequest) ([]client.FirewallRuleRequest, error) {
		i := firewallRuleIndex(rules, priority)
		if i < 0 {
			return nil, notFoundf("firewall %q has no rule at priority %d", args[0], priority)
		}
		if *rules[i].Enabled == enabled {
			return nil, errFirewallUnchanged
		}
		rules[i].Enabled = &enabled
		return rules, nil
	})
}

// errFirewallUnchanged is returned by a rule change that found the rule
// already as asked: nothing is written and the command succeeds.
var errFirewallUnchanged = errors.New("unchanged")

// changeFirewallRules reads a firewall, lets change rewrite its rule list, and
// writes the whole firewall back, since the API replaces rules in full. The
// description and the default action are sent as they were read.
func changeFirewallRules(cmd *cobra.Command, name, done string, change func([]client.FirewallRuleRequest) ([]client.FirewallRuleRequest, error)) error {
	c := getClient()
	fw, err := resolveFirewall(cmd.Context(), c, name)
	if err != nil {
		return err
	}
	req := firewallUpdateOf(fw)
	rules, err := change(req.Rules)
	if errors.Is(err, errFirewallUnchanged) {
		if isStructured(rootCmd.Flag("output").Value.String()) {
			return renderData(fw)
		}
		fmt.Println(mutedStyle.Render(fmt.Sprintf("Firewall %q: already so, nothing changed.", fw.Name)))
		return nil
	}
	if err != nil {
		return err
	}
	req.Rules = rules
	return applyFirewallUpdate(cmd, c, fw, req, done)
}

// applyFirewallUpdate writes req over fw and follows the change to the edge.
func applyFirewallUpdate(cmd *cobra.Command, c *client.Client, fw *client.Firewall, req client.UpdateFirewallRequest, done string) error {
	req.AllowLockout, _ = cmd.Flags().GetBool("allow-lockout")
	updated, err := c.UpdateFirewall(cmd.Context(), fw.ID, req)
	if err != nil {
		return explainFirewallError(err)
	}
	return finishFirewallChange(cmd, c, updated, done)
}

// finishFirewallChange follows a stored change to every target unless told
// not to, then prints the firewall as it stands.
func finishFirewallChange(cmd *cobra.Command, c *client.Client, fw *client.Firewall, done string) error {
	structured := isStructured(rootCmd.Flag("output").Value.String())
	noWait, _ := cmd.Flags().GetBool("no-wait")
	waited := false
	if !noWait && len(fw.Targets) > 0 {
		settled, err := followFirewall(structured, fmt.Sprintf("Waiting for the edge to enforce revision %d...", fw.Revision), func() (*client.Firewall, error) {
			return waitForFirewall(cmd.Context(), c, fw.ID, fw.Revision)
		})
		if err != nil {
			return err
		}
		// The findings are about the content just written, which the update's
		// own answer carries.
		settled.Findings = fw.Findings
		fw, waited = settled, true
	}
	if structured {
		return renderData(fw)
	}
	var state string
	switch {
	case len(fw.Targets) == 0:
		state = fmt.Sprintf("revision %d, attached to nothing", fw.Revision)
	case waited:
		state = fmt.Sprintf("revision %d in force on %s", fw.Revision, firewallTargetNames(fw.Targets))
	default:
		state = fmt.Sprintf("revision %d stored, the edge follows; `fpcloud firewall get %s` shows where it is in force", fw.Revision, fw.Name)
	}
	fmt.Println(successBox.Render(
		lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("✓") +
			fmt.Sprintf(" Firewall %q %s: %s", fw.Name, done, state),
	))
	printFirewallFindings(fw.Findings)
	return nil
}

// followFirewall runs a wait under a progress line, or silently when the
// output is for a program.
func followFirewall[T any](structured bool, title string, wait func() (T, error)) (T, error) {
	var out T
	var err error
	action := func() { out, err = wait() }
	if structured {
		action()
	} else {
		withSpinner(title, action)
	}
	return out, err
}

// firewallWait bounds how long a command waits for the edge to take a change;
// firewallPoll is how often it reads.
var (
	firewallWait = 5 * time.Minute
	firewallPoll = 2 * time.Second
)

// waitForFirewall reads the firewall until every target enforces at least
// revision, and fails as soon as one reports it could not.
func waitForFirewall(ctx context.Context, c *client.Client, id string, revision int) (*client.Firewall, error) {
	deadline := timeNow().Add(firewallWait)
	for {
		fw, err := c.GetFirewall(ctx, id)
		if err != nil {
			return nil, err
		}
		settled, err := firewallSettled(fw, revision)
		if err != nil {
			return nil, err
		}
		if settled {
			return fw, nil
		}
		if timeNow().After(deadline) {
			var behind []string
			for _, t := range fw.Targets {
				if !firewallTargetAt(t.AppliedRevision, t.Status, revision) {
					behind = append(behind, firewallTargetName(t))
				}
			}
			return nil, fmt.Errorf("timed out after %s waiting for revision %d of firewall %q on %s; the change is stored and `fpcloud firewall get %s` shows where it is in force",
				firewallWait, revision, fw.Name, strings.Join(behind, ", "), fw.Name)
		}
		time.Sleep(firewallPoll)
	}
}

// firewallSettled says whether every target of fw enforces at least revision.
// A target that failed is an error carrying its message, whatever the others
// did. A firewall with no targets is settled: nothing enforces it.
func firewallSettled(fw *client.Firewall, revision int) (bool, error) {
	var failed []string
	settled := true
	for _, t := range fw.Targets {
		switch {
		case t.Status == "failed":
			failed = append(failed, fmt.Sprintf("%s: %s", firewallTargetName(t), firstNonEmpty(t.Message, "the edge reported a failure with no message")))
		case !firewallTargetAt(t.AppliedRevision, t.Status, revision):
			settled = false
		}
	}
	if len(failed) > 0 {
		return false, fmt.Errorf("revision %d of firewall %q could not be applied on %s", revision, fw.Name, strings.Join(failed, "; "))
	}
	return settled, nil
}

// firewallTargetAt says whether a target enforces at least revision.
func firewallTargetAt(applied int, status string, revision int) bool {
	return status == "applied" && applied >= revision
}

// waitForFirewallOn follows an attach on the target itself: read returns the
// target's own view of its firewall, and the wait ends once that names
// firewallID enforced at revision or later.
func waitForFirewallOn(target string, read func() (*client.FirewallRef, error), firewallID string, revision int) (*client.FirewallRef, error) {
	deadline := timeNow().Add(firewallWait)
	for {
		ref, err := read()
		if err != nil {
			return nil, err
		}
		if ref != nil && ref.ID == firewallID {
			if ref.Status == "failed" {
				return nil, fmt.Errorf("firewall %q could not be applied on %s: %s", ref.Name, target, firstNonEmpty(ref.Message, "the edge reported a failure with no message"))
			}
			if firewallTargetAt(ref.AppliedRevision, ref.Status, revision) {
				return ref, nil
			}
		}
		if timeNow().After(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting for the firewall to be enforced on %s; it is attached, and the edge has not confirmed it", firewallWait, target)
		}
		time.Sleep(firewallPoll)
	}
}

// waitForFirewallOff follows a detach: the target reports no firewall once
// the edge has stopped enforcing the one it had.
func waitForFirewallOff(target string, read func() (*client.FirewallRef, error)) error {
	deadline := timeNow().Add(firewallWait)
	for {
		ref, err := read()
		if err != nil {
			return err
		}
		if ref == nil {
			return nil
		}
		if ref.Status == "failed" {
			return fmt.Errorf("the firewall could not be taken off %s: %s", target, firstNonEmpty(ref.Message, "the edge reported a failure with no message"))
		}
		if timeNow().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for the firewall to come off %s; it is detached, and the edge has not confirmed it", firewallWait, target)
		}
		time.Sleep(firewallPoll)
	}
}

// firewallAttachment is the --firewall and --no-firewall pair as a command
// that attaches read it.
type firewallAttachment struct {
	Name         string // the firewall to attach, by name or id
	Detach       bool
	AllowLockout bool
	NoWait       bool
}

// firewallAttachFlags declares the attach flags on a command that updates a
// target a firewall can be attached to.
func firewallAttachFlags(cmd *cobra.Command, target string) {
	cmd.Flags().String("firewall", "", "Attach this firewall to the "+target+", replacing any it has")
	cmd.Flags().Bool("no-firewall", false, "Detach the "+target+"'s firewall")
	cmd.Flags().Bool("allow-lockout", false, "Attach a firewall even when it denies the address you are calling from")
	cmd.Flags().Bool("no-wait", false, "With --firewall or --no-firewall: return once the change is accepted, without waiting for the edge to enforce it")
	cmd.MarkFlagsMutuallyExclusive("firewall", "no-firewall")
}

// firewallAttachmentFrom reads the attach flags; nil when neither was given.
func firewallAttachmentFrom(cmd *cobra.Command) (*firewallAttachment, error) {
	name, _ := cmd.Flags().GetString("firewall")
	detach, _ := cmd.Flags().GetBool("no-firewall")
	if cmd.Flags().Changed("firewall") && name == "" {
		return nil, usageError{errors.New("--firewall needs a firewall's name; --no-firewall detaches")}
	}
	if name == "" && !detach {
		return nil, nil
	}
	a := &firewallAttachment{Name: name, Detach: detach}
	a.AllowLockout, _ = cmd.Flags().GetBool("allow-lockout")
	a.NoWait, _ = cmd.Flags().GetBool("no-wait")
	return a, nil
}

// firewallTargetAPI is the three calls an attach makes on one kind of
// target, so apps and websites share the sequence.
type firewallTargetAPI struct {
	Target string // "app web", "website docs": how messages name it
	Attach func(context.Context, client.AttachFirewallRequest) (*client.FirewallRef, error)
	Detach func(context.Context) error
	Read   func(context.Context) (*client.FirewallRef, error)
}

// apply attaches or detaches and, unless told not to, waits for the edge.
func (a *firewallAttachment) apply(ctx context.Context, api firewallTargetAPI, structured bool) error {
	if a.Detach {
		if err := api.Detach(ctx); err != nil {
			return explainFirewallError(err)
		}
		if a.NoWait {
			return nil
		}
		_, err := followFirewall(structured, "Waiting for the edge to stop enforcing the firewall...", func() (struct{}, error) {
			return struct{}{}, waitForFirewallOff(api.Target, func() (*client.FirewallRef, error) { return api.Read(ctx) })
		})
		return err
	}
	ref, err := api.Attach(ctx, client.AttachFirewallRequest{Firewall: a.Name, AllowLockout: a.AllowLockout})
	if err != nil {
		return explainFirewallError(err)
	}
	if a.NoWait {
		return nil
	}
	_, err = followFirewall(structured, fmt.Sprintf("Waiting for the edge to enforce firewall %s...", ref.Name), func() (*client.FirewallRef, error) {
		return waitForFirewallOn(api.Target, func() (*client.FirewallRef, error) { return api.Read(ctx) }, ref.ID, ref.Revision)
	})
	return err
}

// appFirewallAPI is the attach calls for an app.
func appFirewallAPI(c *client.Client, appID string) firewallTargetAPI {
	return firewallTargetAPI{
		Target: "the app",
		Attach: func(ctx context.Context, req client.AttachFirewallRequest) (*client.FirewallRef, error) {
			return c.AttachAppFirewall(ctx, appID, req)
		},
		Detach: func(ctx context.Context) error { return c.DetachAppFirewall(ctx, appID) },
		Read: func(ctx context.Context) (*client.FirewallRef, error) {
			app, err := c.GetApp(ctx, appID)
			if err != nil {
				return nil, err
			}
			return app.Firewall, nil
		},
	}
}

// websiteFirewallAPI is the attach calls for a bucket's website.
func websiteFirewallAPI(c *client.Client, bucketID string) firewallTargetAPI {
	return firewallTargetAPI{
		Target: "the website",
		Attach: func(ctx context.Context, req client.AttachFirewallRequest) (*client.FirewallRef, error) {
			return c.AttachWebsiteFirewall(ctx, bucketID, req)
		},
		Detach: func(ctx context.Context) error { return c.DetachWebsiteFirewall(ctx, bucketID) },
		Read: func(ctx context.Context) (*client.FirewallRef, error) {
			b, err := c.GetBucket(ctx, bucketID)
			if err != nil {
				return nil, err
			}
			return b.Firewall, nil
		},
	}
}

// firewallRefDisplay renders a target's own view of its firewall for an info
// box: the name, and how far the edge is with it.
func firewallRefDisplay(ref *client.FirewallRef) string {
	switch {
	case ref == nil:
		return mutedStyle.Render("none")
	case ref.ID == "":
		return firewallStatus("pending") + mutedStyle.Render(" being detached")
	case firewallTargetAt(ref.AppliedRevision, ref.Status, ref.Revision):
		return fmt.Sprintf("%s (revision %d) %s", ref.Name, ref.Revision, firewallStatus(ref.Status))
	}
	out := fmt.Sprintf("%s (revision %d, the edge enforces %d) %s", ref.Name, ref.Revision, ref.AppliedRevision, firewallStatus(ref.Status))
	if ref.Message != "" {
		out += "\n" + ref.Message
	}
	return out
}

// resolveFirewall finds a firewall in the current project by name or id. The
// list carries each firewall's rules and targets, and not its findings.
func resolveFirewall(ctx context.Context, c *client.Client, name string) (*client.Firewall, error) {
	projectID, err := requireProject()
	if err != nil {
		return nil, err
	}
	firewalls, err := c.ListFirewalls(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, fw := range firewalls {
		if fw.Name == name || fw.ID == name {
			return fw, nil
		}
	}
	return nil, notFoundf("no firewall %q in this project", name)
}

// firewallUpdateOf is the update that changes nothing: the firewall's own
// description, default action and rules, each rule's enabled stated.
func firewallUpdateOf(fw *client.Firewall) client.UpdateFirewallRequest {
	req := client.UpdateFirewallRequest{
		Description:   fw.Description,
		DefaultAction: fw.DefaultAction,
		Rules:         make([]client.FirewallRuleRequest, len(fw.Rules)),
	}
	for i, r := range fw.Rules {
		enabled := r.Enabled
		req.Rules[i] = client.FirewallRuleRequest{
			Priority:    r.Priority,
			Action:      r.Action,
			Description: r.Description,
			Enabled:     &enabled,
			Match:       client.FirewallMatch{Sources: append([]string(nil), r.Match.Sources...)},
		}
	}
	return req
}

func firewallRuleIndex(rules []client.FirewallRuleRequest, priority int) int {
	for i, r := range rules {
		if r.Priority == priority {
			return i
		}
	}
	return -1
}

func firewallPriorityArg(arg string) (int, error) {
	priority, err := strconv.Atoi(arg)
	if err != nil {
		return 0, usageError{fmt.Errorf("priority %q is not a number; a rule is named by its priority", arg)}
	}
	return priority, nil
}

func checkFirewallAction(what, action string) error {
	if action != "allow" && action != "deny" {
		return usageError{fmt.Errorf("%s takes allow or deny, not %q", what, action)}
	}
	return nil
}

// checkFirewallSources refuses what is not an address range before a request
// is made: a CIDR, or a bare address standing for the range holding only it.
func checkFirewallSources(sources []string) error {
	if len(sources) == 0 {
		return errors.New("a rule needs at least one address range")
	}
	for _, s := range sources {
		if _, err := netip.ParsePrefix(s); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(s); err == nil {
			continue
		}
		return fmt.Errorf("%q is not an address range; write a CIDR such as 198.51.100.0/24 or 2001:db8::/32, or a bare address", s)
	}
	return nil
}

// parseFirewallRuleFlag reads one --rule value: "<priority> <allow|deny>
// <cidr>[,<cidr>...]". Exactly three fields, so a space after a comma is
// refused instead of being read as something else.
func parseFirewallRuleFlag(spec string) (client.FirewallRuleRequest, error) {
	const form = `write it as "<priority> <allow|deny> <cidr>[,<cidr>...]", for example "100 allow 198.51.100.0/24,2001:db8::/32"`
	fields := strings.Fields(spec)
	if len(fields) != 3 {
		return client.FirewallRuleRequest{}, fmt.Errorf("--rule %q has %d fields, not three; %s", spec, len(fields), form)
	}
	priority, err := strconv.Atoi(fields[0])
	if err != nil {
		return client.FirewallRuleRequest{}, fmt.Errorf("--rule %q: priority %q is not a number; %s", spec, fields[0], form)
	}
	if fields[1] != "allow" && fields[1] != "deny" {
		return client.FirewallRuleRequest{}, fmt.Errorf("--rule %q: action %q is not allow or deny; %s", spec, fields[1], form)
	}
	sources := strings.Split(fields[2], ",")
	if err := checkFirewallSources(sources); err != nil {
		return client.FirewallRuleRequest{}, fmt.Errorf("--rule %q: %w", spec, err)
	}
	return client.FirewallRuleRequest{
		Priority: priority,
		Action:   fields[1],
		Match:    client.FirewallMatch{Sources: sources},
	}, nil
}

// parseFirewallRuleFlags reads every --rule, refusing two at one priority:
// the priority is the rule's identity, so the second would replace the first.
func parseFirewallRuleFlags(specs []string) ([]client.FirewallRuleRequest, error) {
	var rules []client.FirewallRuleRequest
	for _, spec := range specs {
		rule, err := parseFirewallRuleFlag(spec)
		if err != nil {
			return nil, usageError{err}
		}
		if firewallRuleIndex(rules, rule.Priority) >= 0 {
			return nil, usageError{fmt.Errorf("--rule: priority %d is given twice; a priority names one rule", rule.Priority)}
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// firewallFile is what -f reads. Description is a pointer so a file that
// leaves it out keeps the firewall's own on an update. The remaining fields
// are what a firewall read carries beyond its content; they are accepted and
// ignored so the output of firewall get can be written back, while a key that
// is neither is refused as a likely typo.
type firewallFile struct {
	Description   *string                      `json:"description"`
	DefaultAction string                       `json:"default_action"`
	Rules         []client.FirewallRuleRequest `json:"rules"`

	ID        any `json:"id"`
	ProjectID any `json:"project_id"`
	Name      any `json:"name"`
	Revision  any `json:"revision"`
	Targets   any `json:"targets"`
	Findings  any `json:"findings"`
	CreatedAt any `json:"created_at"`
	UpdatedAt any `json:"updated_at"`
}

// readFirewallFile reads a firewall's content from a YAML or JSON file, or
// from stdin when path is "-".
func readFirewallFile(path string, stdin io.Reader) (*firewallFile, error) {
	var data []byte
	var err error
	if path == "-" {
		path = "standard input"
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read the firewall file: %w", err)
	}
	var file firewallFile
	if err := yaml.UnmarshalStrict(data, &file); err != nil {
		return nil, fmt.Errorf("%s is not a firewall file: %w", path, err)
	}
	if file.DefaultAction != "" && file.DefaultAction != "allow" && file.DefaultAction != "deny" {
		return nil, fmt.Errorf("%s: default_action is allow or deny, not %q", path, file.DefaultAction)
	}
	for i, r := range file.Rules {
		if r.Action != "allow" && r.Action != "deny" {
			return nil, fmt.Errorf("%s: rules[%d].action is allow or deny, not %q", path, i, r.Action)
		}
		if err := checkFirewallSources(r.Match.Sources); err != nil {
			return nil, fmt.Errorf("%s: rules[%d].match.sources: %w", path, i, err)
		}
	}
	return &file, nil
}

// explainFirewallError adds what to do about a refusal the API names by
// code. The API's error stays wrapped, so the exit status is still its own.
func explainFirewallError(err error) error {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Code {
	case "FIREWALL_DENIES_CALLER":
		return fmt.Errorf("%w\nNothing was changed: this would deny the address you are calling from. Pass --allow-lockout to make the change anyway.", err)
	case "FIREWALL_EDGE_NOT_READY":
		return fmt.Errorf("%w\nNothing was changed: the edge cannot take a firewall right now. Run the command again in a moment.", err)
	}
	return err
}

// firewallDetachHint names the commands that detach a firewall from each of
// its targets, for a delete refused because it is still attached.
func firewallDetachHint(fw *client.Firewall) string {
	if len(fw.Targets) == 0 {
		return "Detach it from every app and website first; fpcloud firewall get " + fw.Name + " lists them."
	}
	lines := []string{"Detach it first:"}
	for _, t := range fw.Targets {
		if t.Type == "bucket" {
			lines = append(lines, fmt.Sprintf("  fpcloud website update %s --no-firewall", t.Name))
		} else {
			lines = append(lines, fmt.Sprintf("  fpcloud app update %s --no-firewall", t.Name))
		}
	}
	return strings.Join(lines, "\n")
}

// printFirewall is firewall get for a person: the firewall, its rules in the
// order they are evaluated, where it is attached, and its findings.
func printFirewall(fw *client.Firewall) {
	fmt.Println(renderInfoBox("Firewall", [][]string{
		{"ID", mutedStyle.Render(fw.ID)},
		{"Name", fw.Name},
		{"Description", dashIfBlank(fw.Description)},
		{"Default action", firewallAction(fw.DefaultAction)},
		{"Revision", strconv.Itoa(fw.Revision)},
		{"Updated", fw.UpdatedAt.Format("2006-01-02 15:04")},
	}))

	rules := append([]client.FirewallRule(nil), fw.Rules...)
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
	if len(rules) == 0 {
		fmt.Println(mutedStyle.Render(fmt.Sprintf("  No rules: every request gets the default action, %s.", fw.DefaultAction)))
	} else {
		rows := make([][]string, len(rules))
		for i, r := range rules {
			rows[i] = []string{
				strconv.Itoa(r.Priority),
				firewallAction(r.Action),
				strings.Join(r.Match.Sources, ", "),
				yesNo(r.Enabled),
				dashIfBlank(r.Description),
			}
		}
		renderTable([]string{"PRIORITY", "ACTION", "SOURCES", "ENABLED", "DESCRIPTION"}, rows)
	}

	if len(fw.Targets) == 0 {
		fmt.Println(mutedStyle.Render("  Attached to nothing, so it is enforced nowhere."))
	} else {
		rows := make([][]string, len(fw.Targets))
		for i, t := range fw.Targets {
			status := firewallStatus(t.Status)
			if t.Message != "" {
				status += " " + t.Message
			}
			rows[i] = []string{firewallTargetKind(t.Type), t.Name, strconv.Itoa(t.AppliedRevision), status}
		}
		renderTable([]string{"ATTACHED TO", "NAME", "APPLIED REVISION", "STATUS"}, rows)
	}
	printFirewallFindings(fw.Findings)
}

// printFirewallFindings prints the API's warnings about a firewall on stderr,
// where they stay out of piped output.
func printFirewallFindings(findings []client.FirewallFinding) {
	style := lipgloss.NewStyle().Foreground(colorWarning)
	for _, f := range findings {
		line := "! " + f.Message
		if f.Field != "" {
			line = "! " + f.Field + ": " + f.Message
		}
		fmt.Fprintln(os.Stderr, style.Render(line))
	}
}

// firewallDecisionLine is firewall test for a person: the address, what
// happens to it, and what decided.
func firewallDecisionLine(d *client.FirewallDecision) string {
	if d.Rule == nil {
		return fmt.Sprintf("%s: %s, by default (no rule matches)", d.Address, firewallAction(d.Action))
	}
	by := fmt.Sprintf("rule %d", d.Rule.Priority)
	if d.Rule.Description != "" {
		by += " (" + d.Rule.Description + ")"
	}
	return fmt.Sprintf("%s: %s, by %s", d.Address, firewallAction(d.Action), by)
}

func firewallAction(action string) string {
	switch action {
	case "allow":
		return lipgloss.NewStyle().Foreground(colorSuccess).Render(action)
	case "deny":
		return lipgloss.NewStyle().Foreground(colorDanger).Render(action)
	}
	return action
}

func firewallStatus(status string) string {
	switch status {
	case "applied":
		return lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("● " + status)
	}
	return renderStatus(status)
}

// firewallTargetKind names a target's type as the CLI does: a bucket a
// firewall is attached to is a website.
func firewallTargetKind(kind string) string {
	if kind == "bucket" {
		return "website"
	}
	return kind
}

func firewallTargetName(t client.FirewallTarget) string {
	return firewallTargetKind(t.Type) + " " + t.Name
}

func firewallTargetNames(targets []client.FirewallTarget) string {
	if len(targets) == 0 {
		return mutedStyle.Render("-")
	}
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = firewallTargetName(t)
	}
	return strings.Join(names, ", ")
}

func dashIfBlank(s string) string {
	if s == "" {
		return mutedStyle.Render("-")
	}
	return s
}

func init() {
	firewallCreateCmd.Flags().String("description", "", "What the firewall is for")
	firewallCreateCmd.Flags().String("default-action", "", "What happens to a request no rule matches: allow or deny (default allow)")
	firewallCreateCmd.Flags().StringArray("rule", nil, "A rule as \"<priority> <allow|deny> <cidr>[,<cidr>...]\" (repeatable)")
	firewallCreateCmd.Flags().StringP("file", "f", "", "Read the description, default action and rules from a YAML or JSON file (- for stdin)")
	firewallCreateCmd.MarkFlagsMutuallyExclusive("rule", "file")

	firewallUpdateCmd.Flags().String("description", "", "What the firewall is for")
	firewallUpdateCmd.Flags().String("default-action", "", "What happens to a request no rule matches: allow or deny")
	firewallUpdateCmd.Flags().StringP("file", "f", "", "Replace the rules in full from a YAML or JSON file (- for stdin)")

	firewallRuleAddCmd.Flags().Int("priority", 0, "The rule's place in the order, lower first; unique within the firewall")
	firewallRuleAddCmd.Flags().String("action", "", "allow or deny")
	firewallRuleAddCmd.Flags().StringSlice("source", nil, "Client address range as a CIDR, or a bare address (repeatable, or comma-separated)")
	firewallRuleAddCmd.Flags().String("description", "", "What the rule is for")
	firewallRuleAddCmd.Flags().Bool("disabled", false, "Store the rule without evaluating it")
	for _, name := range []string{"priority", "action", "source"} {
		_ = firewallRuleAddCmd.MarkFlagRequired(name)
	}

	// Every command that changes what the edge enforces waits for it and can
	// be refused for denying the caller, so all of them take both flags.
	for _, cmd := range []*cobra.Command{firewallUpdateCmd, firewallRollbackCmd, firewallRuleAddCmd, firewallRuleRemoveCmd, firewallRuleEnableCmd, firewallRuleDisableCmd} {
		cmd.Flags().Bool("allow-lockout", false, "Make the change even when it denies the address you are calling from")
		cmd.Flags().Bool("no-wait", false, "Return once the change is stored, without waiting for the edge to enforce it")
	}

	firewallRuleCmd.AddCommand(firewallRuleAddCmd, firewallRuleRemoveCmd, firewallRuleEnableCmd, firewallRuleDisableCmd)
	firewallCmd.AddCommand(firewallCreateCmd, firewallListCmd, firewallGetCmd, firewallUpdateCmd, firewallDeleteCmd, firewallRevisionsCmd, firewallRollbackCmd, firewallTestCmd, firewallRuleCmd)
	rootCmd.AddCommand(firewallCmd)
}
