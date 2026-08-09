package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// The surfaces `torio status setup` can configure, in the order `--help` should
// list them.
var statusSurfaces = []string{"tmux", "zsh"}

// newStatusSetupCmd prints the configuration for an ambient status surface.
//
// It prints. It does not write, and it will not grow a flag that does. A
// dotfile is the operator's, and Torio holds the same line about it that
// bootstrap holds about a managed settings file it did not install: report,
// never repair in place. The difference between the two commands is only who
// owns the file.
func newStatusSetupCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "setup <" + strings.Join(statusSurfaces, "|") + ">",
		Short: "Print the configuration that puts the status line on a surface",
		Long: "Print the configuration for an ambient status surface: a tmux status bar, " +
			"or a zsh prompt on a terminal that has no bar.\n\n" +
			"It writes nothing. The output names the file it belongs in and the command " +
			"that reloads it, and what to do with it is the operator's decision.\n\n" +
			"The snippet calls this binary by its own path rather than by name. An older " +
			"`torio` earlier on PATH has no `status` subcommand and exits 2, which every " +
			"one of these surfaces renders as an empty line with no error anywhere the " +
			"operator would look for one. Re-run this after moving or reinstalling Torio.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			surface := args[0]
			snippet, err := statusSetupSnippet(surface, shellQuote(a.executable()))
			if err != nil {
				return usageError(err.Error())
			}
			if a.jsonOut {
				return writeJSON(a.stdout, successEnvelope("status.setup", statusSetupDoc{
					Surface:       surface,
					Configuration: snippet,
				}))
			}
			_, err = fmt.Fprint(a.stdout, snippet)
			return err
		},
	}
}

// statusSetupDoc is what `--json` carries: the same text, addressable by a
// script that wants to place it rather than read it.
type statusSetupDoc struct {
	Surface       string `json:"surface"`
	Configuration string `json:"configuration"`
}

func statusSetupSnippet(surface, command string) (string, error) {
	switch surface {
	case "tmux":
		return fmt.Sprintf(tmuxSetup, command), nil
	case "zsh":
		return fmt.Sprintf(zshSetup, command), nil
	default:
		return "", fmt.Errorf("unknown surface %q; known surfaces are %s",
			surface, strings.Join(statusSurfaces, ", "))
	}
}

// executable is the path to this binary, for a snippet that must not depend on
// what PATH resolves later. It falls back to the bare name, which is still a
// working recipe on a host where Torio is installed once.
func (a *app) executable() string {
	if a.executablePath == nil {
		a.executablePath = os.Executable
	}
	p, err := a.executablePath()
	if err != nil || p == "" {
		return "torio"
	}
	return p
}

// shellQuote quotes a path for /bin/sh, which is what runs it in both snippets:
// tmux passes `#()` to a shell, and zsh runs the refresh function.
func shellQuote(s string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("_@%+=:,./-", r)
	}
	quote := s == ""
	for _, r := range s {
		if !safe(r) {
			quote = true
			break
		}
	}
	if !quote {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

const tmuxSetup = `# Torio ambient status. Add to ~/.tmux.conf, then: tmux source-file ~/.tmux.conf
# Nothing here writes to that file; this is text to place, not a change to apply.

# The chips are drawn for a dark bar. Drop these two lines if your theme sets one.
set -g default-terminal "tmux-256color"
set -g status-style bg=#1b2032,fg=#ccd3e8

# tmux truncates the right-hand status at 40 characters, and two boxes already
# exceed that: the end of the line goes missing without saying so.
set -g status-right-length 120

# The poll is one host-side list plus a few small guest commands per running box.
# Pick an interval you are content to run forever, not the smallest one that works.
set -g status-interval 15
set -g status-right "#(%s status --format=tmux)"
`

const zshSetup = `# Torio ambient status. Add to ~/.zshrc, then: exec zsh
# Nothing here writes to that file; this is text to place, not a change to apply.

# A prompt is rendered synchronously and this poll enters VMs, so it must not run
# in one. Each shell gets a private, unpredictable cache file; refreshes happen
# while commands run, and the prompt only reads the last completed poll.
typeset -g TORIO_STATUS_CACHE
TORIO_STATUS_CACHE=$(umask 077; command mktemp "${TMPDIR:-/tmp}/torio-status.XXXXXX") || return

torio_status_refresh() {
  local lock="$TORIO_STATUS_CACHE.lock"
  command mkdir -- "$lock" 2>/dev/null || return 0

  # The rename is unconditional. A poll that failed says so on the line it wrote,
  # and a prompt still showing the last good answer would be confidently wrong.
  ( local tmp=''
    trap '[[ -z "$tmp" ]] || command rm -f -- "$tmp"; command rmdir -- "$lock"' EXIT
    tmp=$(umask 077; command mktemp "$TORIO_STATUS_CACHE.XXXXXX") || exit
    %s status --format=prompt >"$tmp" 2>/dev/null
    command mv -f -- "$tmp" "$TORIO_STATUS_CACHE"
    tmp=''
  ) &!
}

torio_status_prompt() {
  local line=''
  IFS= read -r line <"$TORIO_STATUS_CACHE" 2>/dev/null || true
  # Prompt escapes belong to zsh, not to a status value. Doubling a percent
  # keeps even a custom instance identifier literal without PROMPT_SUBST.
  line=${line//\%%/%%%%}
  RPROMPT="%%F{244}${line}%%f"
}
autoload -Uz add-zsh-hook
add-zsh-hook preexec torio_status_refresh
add-zsh-hook precmd torio_status_prompt

# Seed the first prompt without blocking it. Later polls normally run while the
# foreground command is using the terminal.
torio_status_refresh
`
