# Renders `fp <org>:<project>` for a shell prompt, read from whichever
# config.yaml the current directory resolves to. A prompt hook sources this file
# once and calls prompt_segment; the renderer itself comes out of the fpcloud
# binary on first call, so the segment tracks the binary rather than drifting
# from a committed copy of its output.
#
# The sentinel makes a missing binary recoverable: without it, a shell entered
# before fpcloud was installed would render nothing until it was replaced.
prompt_segment() {
  [ -z "$_fpcloud_prompt_loading" ] || return 0
  command -v fpcloud >/dev/null 2>&1 || return 0
  _fp_snippet=$(fpcloud prompt-segment zsh 2>/dev/null) || return 0
  eval "$_fp_snippet" 2>/dev/null || return 0
  _fpcloud_prompt_loading=1
  prompt_segment "$@"
  unset _fpcloud_prompt_loading
}
