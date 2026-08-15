package cli

import (
	"io"

	"github.com/spf13/cobra"
)

// promptSegmentScript defines `prompt_segment`, which renders the active
// context as `fp <org>:<project>` — the segment a shell prompt embeds.
//
// It is emitted rather than executed so nothing spawns a process per prompt:
// the shell loads this once and calls a function from then on. The org and
// project are read straight out of config.yaml, resolving the directory the
// same way the CLI does (FPCLOUD_CONFIG_DIR, else FPCLOUD_STATE_DIR, else
// ~/.fpcloud) — so a directory-scoped context renders as that context.
//
// The escapes are real ANSI sequences, not zsh `%F{…}` prompt escapes: the
// caller decides where the segment goes and may not be putting it in PS1.
const promptSegmentScript = `_fpcloud_prompt_read() {
	_fp_file="${FPCLOUD_CONFIG_DIR:-${FPCLOUD_STATE_DIR:-$HOME/.fpcloud}}/config.yaml"
	[ -r "$_fp_file" ] || return 0
	sed -n "s/^$1:[[:space:]]*//p" "$_fp_file" 2>/dev/null | head -n1 |
		sed -e 's/^["'"'"']//' -e 's/["'"'"']$//' -e 's/[[:space:]]*$//'
}

prompt_segment() {
	_fp_org=$(_fpcloud_prompt_read current_org)
	_fp_project=$(_fpcloud_prompt_read current_project)
	[ -n "$_fp_org" ] || [ -n "$_fp_project" ] || return 0
	printf '\033[38;5;244m%s\033[0m \033[38;5;37m%s\033[0m\033[38;5;244m:\033[0m\033[38;5;208m%s\033[0m' \
		"${FPCLOUD_PROMPT_SYMBOL:-fp}" "${_fp_org:--}" "${_fp_project:--}"
}
`

var promptSegmentCmd = &cobra.Command{
	Use:   "prompt-segment",
	Short: "Print the shell snippet that renders your org and project in the prompt",
	Long: `Print a shell snippet defining prompt_segment, which renders the active
org and project as ` + "`fp <org>:<project>`" + `.

Load it once and embed the function in your prompt:

  source <(fpcloud prompt-segment)
  # zsh
  setopt prompt_subst
  RPROMPT='$(prompt_segment)'
  # bash
  PS1='$(prompt_segment) '"$PS1"

It prints nothing when no org or project is selected, so the segment
disappears outside a Fogpipe context rather than showing placeholders.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := io.WriteString(cmd.OutOrStdout(), promptSegmentScript)
		return err
	},
}

func init() {
	rootCmd.AddCommand(promptSegmentCmd)
}
