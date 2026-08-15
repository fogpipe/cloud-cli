package cli

import (
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// promptSegmentBody defines `prompt_segment`, which renders the active context
// as `fp <org>:<project>` — the segment a shell prompt embeds. COLOURS is
// replaced with the printf format for the shell it is emitted for.
//
// It is emitted rather than executed so nothing spawns a process per prompt:
// the shell loads this once and calls a function from then on. Every value it
// depends on is read inside the function body, so a shell that loaded it in
// one directory renders the right thing in the next one.
//
// The org and project come straight out of config.yaml, resolving the
// directory the same way the CLI does (FPCLOUD_CONFIG_DIR, else
// FPCLOUD_STATE_DIR, else ~/.fpcloud) — so a directory-scoped context renders
// as that context.
const promptSegmentBody = `_fpcloud_prompt_read() {
	_fp_file="${FPCLOUD_CONFIG_DIR:-${FPCLOUD_STATE_DIR:-$HOME/.fpcloud}}/config.yaml"
	[ -r "$_fp_file" ] || return 0
	sed -n "s/^$1:[[:space:]]*//p" "$_fp_file" 2>/dev/null | head -n1 |
		sed -e 's/^["'"'"']//' -e 's/["'"'"']$//' -e 's/[[:space:]]*$//'
}

prompt_segment() {
	_fp_org=$(_fpcloud_prompt_read current_org)
	_fp_project=$(_fpcloud_prompt_read current_project)
	[ -n "$_fp_org" ] || [ -n "$_fp_project" ] || return 0
	printf 'COLOURS' \
		"${FPCLOUD_PROMPT_SYMBOL:-fp}" "${_fp_org:--}" "${_fp_project:--}"
}
`

// Colouring is per shell because the caller decides where the segment goes.
// zsh gets its own prompt escapes: dropped into RPROMPT, raw ANSI would be
// counted as printable and throw the prompt's width off.
const (
	promptColoursZsh   = `%%F{244}%s%%f %%F{37}%s%%f%%F{244}:%%f%%F{208}%s%%f`
	promptColoursPlain = `\033[38;5;244m%s\033[0m \033[38;5;37m%s\033[0m\033[38;5;244m:\033[0m\033[38;5;208m%s\033[0m`
)

func promptSegmentScript(shell string) string {
	colours := promptColoursPlain
	if shell == "zsh" {
		colours = promptColoursZsh
	}
	return strings.Replace(promptSegmentBody, "COLOURS", colours, 1)
}

var promptSegmentCmd = &cobra.Command{
	Use:       "prompt-segment [bash|zsh]",
	Short:     "Print the shell snippet that renders your org and project in the prompt",
	ValidArgs: []string{"bash", "zsh"},
	Args:      cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs),
	Long: `Print a shell snippet defining prompt_segment, which renders the active
org and project as ` + "`fp <org>:<project>`" + `.

Load it once and embed the function in your prompt:

  # zsh
  source <(fpcloud prompt-segment zsh)
  setopt prompt_subst
  RPROMPT='$(prompt_segment)'

  # bash
  source <(fpcloud prompt-segment bash)
  PS1='$(prompt_segment) '"$PS1"

It prints nothing when no org or project is selected, so the segment
disappears outside a Fogpipe context rather than showing placeholders.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		shell := "bash"
		if len(args) == 1 {
			shell = args[0]
		}
		_, err := io.WriteString(cmd.OutOrStdout(), promptSegmentScript(shell))
		return err
	},
}

func init() {
	rootCmd.AddCommand(promptSegmentCmd)
}
