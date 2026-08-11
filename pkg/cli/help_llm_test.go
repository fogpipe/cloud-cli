package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRenderLLMHelp(t *testing.T) {
	root := &cobra.Command{Use: "demo", Short: "demo root"}
	root.PersistentFlags().String("api-url", "http://x", "API server URL")

	create := &cobra.Command{
		Use:     "create <name>",
		Short:   "create a thing",
		Example: "demo create foo --size big",
		Run:     func(*cobra.Command, []string) {},
	}
	create.Flags().String("size", "small", "size of thing")
	create.Flags().Bool("wait", false, "wait for ready")

	deploy := &cobra.Command{Use: "deploy", Short: "deploy a thing", Run: func(*cobra.Command, []string) {}}
	deploy.Flags().String("image", "", "image ref")
	_ = deploy.MarkFlagRequired("image")

	root.AddCommand(create, deploy)

	out := renderLLMHelp(root)

	if strings.Contains(out, "\x1b[") {
		t.Fatalf("output contains ANSI escape codes:\n%s", out)
	}
	if got := strings.Count(out, "--api-url"); got != 1 {
		t.Errorf("global flag --api-url should appear exactly once, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "--size string (default=small)") {
		t.Errorf("expected size flag with type+default, output:\n%s", out)
	}
	if !strings.Contains(out, "--image string (required)") {
		t.Errorf("expected required image flag, output:\n%s", out)
	}
	if !strings.Contains(out, "example: demo create foo --size big") {
		t.Errorf("expected the leaf example, output:\n%s", out)
	}
	// create sorts before deploy (stable tree order).
	if strings.Index(out, "demo create") > strings.Index(out, "demo deploy") {
		t.Errorf("commands not in stable sorted order:\n%s", out)
	}
}

func TestHasHelpLLMFlag(t *testing.T) {
	if !hasHelpLLMFlag([]string{"db", "--help-llm"}) {
		t.Error("should detect --help-llm after subcommand")
	}
	if !hasHelpLLMFlag([]string{"--help-llm"}) {
		t.Error("should detect bare --help-llm")
	}
	if hasHelpLLMFlag([]string{"--help"}) {
		t.Error("must not treat --help as --help-llm")
	}
}
