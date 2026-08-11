package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
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
		case "get-token", "upgrade", "version":
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

	rootCmd.PersistentFlags().String("api-url", defaultURL, "API server URL")
	rootCmd.PersistentFlags().String("api-key", defaultKey, "API key for authentication")
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

	registerCompletions()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if isUsageError(err) {
			fmt.Fprintln(os.Stderr, errorHelpHint)
		}
		os.Exit(1)
	}
}
