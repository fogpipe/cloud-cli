package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// hasHelpLLMFlag reports whether --help-llm appears anywhere in the args. It is
// detected in main() before cobra.Execute because cobra special-cases only --help.
func hasHelpLLMFlag(args []string) bool {
	for _, a := range args {
		if a == "--help-llm" || strings.HasPrefix(a, "--help-llm=") {
			return true
		}
	}
	return false
}

// renderLLMHelp produces dense, plain-text help for scope and its whole subtree:
// every command, every flag (type/default/required), and one example per leaf.
// Global (persistent) flags are stated once at the top, not repeated per command.
func renderLLMHelp(scope *cobra.Command) string {
	var b strings.Builder
	root := scope.Root()

	fmt.Fprintf(&b, "fpcloud %s — LLM help (dense, plain-text). Scope: %s\n", version, scope.CommandPath())
	b.WriteString("Every command + flag (type/default/required) and one example per leaf. Global flags listed once.\n\n")

	globals := map[string]bool{"help": true, "help-llm": true}
	b.WriteString("GLOBAL FLAGS (valid on every command):\n")
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		globals[f.Name] = true
		b.WriteString("  " + flagLine(f) + "\n")
	})
	b.WriteString("  --help bool  human-readable help for a command\n")
	b.WriteString("  --help-llm bool  this dense help for a command and its subtree\n\n")

	b.WriteString("COMMANDS:\n\n")
	writeCmdLLM(&b, scope, globals)
	return b.String()
}

func writeCmdLLM(b *strings.Builder, cmd *cobra.Command, globals map[string]bool) {
	if cmd.Hidden {
		return
	}

	fmt.Fprintf(b, "%s — %s\n", cmd.CommandPath(), cmd.Short)
	if cmd.Runnable() {
		fmt.Fprintf(b, "  usage: %s\n", cmd.UseLine())
	}

	var locals []*pflag.Flag
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || globals[f.Name] {
			return
		}
		locals = append(locals, f)
	})
	if len(locals) > 0 {
		b.WriteString("  flags:\n")
		for _, f := range locals {
			b.WriteString("    " + flagLine(f) + "\n")
		}
	}

	if ex := firstExampleLine(cmd.Example); ex != "" {
		fmt.Fprintf(b, "  example: %s\n", ex)
	}
	b.WriteString("\n")

	children := cmd.Commands()
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, ch := range children {
		if ch.Hidden || ch.Name() == "help" || ch.Name() == "completion" {
			continue
		}
		writeCmdLLM(b, ch, globals)
	}
}

// flagLine renders one flag as: --name[, -x] TYPE (required|default=X)  usage
func flagLine(f *pflag.Flag) string {
	s := "--" + f.Name
	if f.Shorthand != "" {
		s += ", -" + f.Shorthand
	}
	s += " " + f.Value.Type()
	switch {
	case isRequiredFlag(f):
		s += " (required)"
	case f.DefValue != "" && f.DefValue != "false":
		s += " (default=" + f.DefValue + ")"
	}
	if f.Usage != "" {
		s += "  " + f.Usage
	}
	return s
}

func isRequiredFlag(f *pflag.Flag) bool {
	if f.Annotations == nil {
		return false
	}
	_, ok := f.Annotations[cobra.BashCompOneRequiredFlag]
	return ok
}

func firstExampleLine(ex string) string {
	for _, line := range strings.Split(ex, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
