package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fogpipe/cloud-cli/pkg/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const errorHelpHint = "Run with --help for usage, or --help-llm for dense, machine-readable help (LLM/agent-oriented)."

type usageError struct{ error }

func isUsageError(err error) bool {
	var ue usageError
	if errors.As(err, &ue) {
		return true
	}
	_, _, ferr := rootCmd.Find(os.Args[1:])
	return ferr != nil
}

// Injected at build time via -ldflags
// "-X github.com/fogpipe/cloud-cli/pkg/cli.version=..." from the git tag (see `just
// build-fpcloud` and .github/workflows/release-fpcloud.yml). Defaults to "dev"
// for plain `go build`.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "fpcloud",
	Short: "Fogpipe CLI",
	Long: lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render("fpcloud") +
		" — Fogpipe Platform CLI\n\n" +
		"Deploy apps, manage databases, and configure domains\non European infrastructure.",
	Version:      version,
	SilenceUsage: true,
	// Validate the output format, then warn (to stderr) when a newer version is
	// available. The version check is skipped for machine-consumed commands so it
	// never adds latency or noise to scripted use.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		format := cmd.Flag("output").Value.String()
		if !isValidOutputFormat(format) {
			return fmt.Errorf("invalid output format %q (valid: %s)", format, strings.Join(outputFormats, ", "))
		}
		switch cmd.Name() {
		case "get-token", "upgrade":
			return nil
		}
		warnIfOutdated()
		return nil
	},
}

func init() {
	cfg, _ := loadConfig()

	// Released binaries default to the prod control plane; a plain `go build`
	// (version "dev") defaults to localhost. Config and --api-url override either.
	defaultURL := "https://api.cloud.fogpipe.com"
	if version == "dev" {
		defaultURL = "http://localhost:8080"
	}
	if cfg != nil && cfg.APIUrl != "" {
		defaultURL = cfg.APIUrl
	}

	defaultKey := ""
	if cfg != nil && cfg.APIKey != "" {
		defaultKey = cfg.APIKey
	}

	defaultProject := ""
	if cfg != nil && cfg.CurrentProject != "" {
		defaultProject = cfg.CurrentProject
	}

	defaultOrg := ""
	if cfg != nil && cfg.CurrentOrg != "" {
		defaultOrg = cfg.CurrentOrg
	}

	rootCmd.PersistentFlags().String("api-url", defaultURL, "API server URL (else FPCLOUD_API_URL, or config.yaml)")
	rootCmd.PersistentFlags().String("api-key", defaultKey, "API key for authentication (else FPCLOUD_API_KEY, config.yaml, or the fpcloud login)")
	rootCmd.PersistentFlags().String("org", defaultOrg, "Current organization")
	rootCmd.PersistentFlags().String("project", defaultProject, "Current project")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "Output format (table, json, yaml)")
	rootCmd.PersistentFlags().Bool("help-llm", false, "Output dense, LLM-oriented help for the command and its subtree")

	rootCmd.SetHelpTemplate("LLM/agent? Run with --help-llm instead of --help for dense, " +
		"machine-readable help covering this command and its whole subtree.\n\n" +
		rootCmd.HelpTemplate())

	rootCmd.SilenceErrors = true
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageError{err}
	})
}

// unknownSubcommand reports a typo under a group: `fpcloud project show` names
// no command, and cobra answers it with the group's help and a nil error — a
// usage error that exits 0 — because it rejects unknown subcommands only at the
// root and a non-runnable command is helped before its Args are checked. Found
// before cobra runs, from the first positional word left after the group.
func unknownSubcommand(args []string) (group *cobra.Command, word string, ok bool) {
	cmd, rest, err := rootCmd.Find(args)
	if err != nil || cmd == rootCmd || cmd.Runnable() || !cmd.HasSubCommands() {
		return nil, "", false
	}
	flags := cmd.Flags()
	flags.AddFlagSet(cmd.InheritedFlags())
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "--" {
			return nil, "", false
		}
		if !strings.HasPrefix(a, "-") {
			return cmd, a, true
		}
		if strings.Contains(a, "=") {
			continue
		}
		var f *pflag.Flag
		if strings.HasPrefix(a, "--") {
			f = flags.Lookup(strings.TrimPrefix(a, "--"))
		} else if len(a) == 2 {
			f = flags.ShorthandLookup(a[1:])
		}
		if f != nil && f.NoOptDefVal == "" {
			i++ // the flag's value
		}
	}
	return nil, "", false
}

// NewRootCommand returns the fully registered command tree.
//
// Exported so the docs pool can be validated against the real cobra
// registration from outside this module: the pool lives in the platform repo
// and the tree lives here, and a check that owns only one of the two is the
// drift it exists to catch.
func NewRootCommand() *cobra.Command { return rootCmd }

// Execute runs the CLI. cmd/fpcloud is a thin main over this.
func Execute() {
	// When invoked as docker-credential-fpcloud (the symlink `fpcloud auth
	// configure-docker` installs), act as a Docker credential helper and exit
	// before cobra so stdout carries only the protocol JSON.
	if isDockerCredentialHelper(os.Args[0]) {
		os.Exit(runDockerCredentialHelper(os.Args[1:]))
	}

	// --help-llm short-circuits before Execute (cobra only special-cases --help):
	// resolve the targeted command and dump dense help for its subtree.
	if hasHelpLLMFlag(os.Args[1:]) {
		cmd, _, err := rootCmd.Find(os.Args[1:])
		if err != nil || cmd == nil {
			cmd = rootCmd
		}
		fmt.Print(renderLLMHelp(cmd))
		return
	}

	if cmd, _, err := rootCmd.Find(os.Args[1:]); err == nil && cmd == rootCmd && hasVersionFlag(os.Args[1:]) {
		warnIfOutdated()
	}

	registerCompletions()
	if group, word, ok := unknownSubcommand(os.Args[1:]); ok {
		fmt.Fprintf(os.Stderr, "unknown command %q for %q\n%s\n", word, group.CommandPath(), errorHelpHint)
		os.Exit(exitUsage)
	}
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		// A deployment that refuses this binary has already said why; what it
		// cannot say is how to fix it, and the fix is never the command the user
		// was typing. Suppress the usage hint in that case — the flags were fine.
		if errors.Is(err, client.ErrClientTooOld) {
			fmt.Fprintln(os.Stderr, "Run `fpcloud upgrade` to install the newest release.")
			os.Exit(exitCode(err))
		}
		// The API can only report the header it did not get; it cannot know we
		// never found a credential to put there. Say so, or a caller reads a
		// rejected request as a malformed one.
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized && resolveAPIKey() == "" {
			fmt.Fprintln(os.Stderr, "No credential found — run `fpcloud login`, or set FPCLOUD_API_KEY (in CI, from the `fogpipe/cloud-actions/auth` step).")
			os.Exit(exitCode(err))
		}
		if isUsageError(err) {
			fmt.Fprintln(os.Stderr, errorHelpHint)
		}
		os.Exit(exitCode(err))
	}
}
