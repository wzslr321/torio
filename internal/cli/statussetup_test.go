package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wzslr321/torio/internal/lima"
)

const testExecutable = "/opt/torio/bin/torio"

func runSetup(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	a := &app{
		stdout:             &stdout,
		stderr:             &stderr,
		build:              testBuild(),
		newLima:            func() *lima.Adapter { return lima.New(&fakeLimaRunner{}) },
		lookupOperatorUser: func() (string, error) { return "testop", nil },
		executablePath:     func() (string, error) { return testExecutable, nil },
	}
	code := runWithApp(context.Background(), a, args)
	return code, stdout.String(), stderr.String()
}

// The snippet names the binary that printed it, not `torio`. This is the
// failure it removes: an older Torio earlier on PATH has no status subcommand
// and exits 2, and every surface renders that as an empty line — no error on
// any stream, nothing in a log, nothing to debug from.
func TestSetupNamesTheBinaryThatPrintedIt(t *testing.T) {
	for _, surface := range statusSurfaces {
		code, stdout, stderr := runSetup(t, []string{"status", "setup", surface})

		if code != int(ExitOK) {
			t.Fatalf("%s: exit = %d, want 0; stderr=%q", surface, code, stderr)
		}
		if !strings.Contains(stdout, testExecutable+" status --format=") {
			t.Errorf("%s: snippet = %q, want it to call %q", surface, stdout, testExecutable)
		}
		if regexp.MustCompile(`(^|[^/\w-])torio status`).MatchString(stdout) {
			t.Errorf("%s: snippet calls torio by name; PATH decides which build that is", surface)
		}
	}
}

func TestSetupDoesNotLoadRuntimeConfiguration(t *testing.T) {
	code, stdout, stderr := runSetup(t, []string{"status", "setup", "tmux", "--config", "/dev/null"})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "status --format=tmux") {
		t.Errorf("stdout = %q, want the requested snippet", stdout)
	}
}

// The snippets and the flag are one feature in two places. A format renamed on
// one side would print a configuration that renders an empty surface, which is
// the exact failure mode nothing reports.
func TestSetupAsksForAFormatThisBuildHas(t *testing.T) {
	format := regexp.MustCompile(`--format=(\w+)`)
	for _, surface := range statusSurfaces {
		_, stdout, _ := runSetup(t, []string{"status", "setup", surface})

		m := format.FindStringSubmatch(stdout)
		if m == nil {
			t.Fatalf("%s: snippet asks for no format: %q", surface, stdout)
		}
		if m[1] == formatTable {
			t.Errorf("%s: snippet asks for the table format, which is not one line", surface)
		}
		if !slices.Contains(statusFormats, m[1]) {
			t.Errorf("%s: snippet asks for --format=%s, which this build does not have", surface, m[1])
		}
	}
}

// It prints; it does not write. The snippet has to say where it belongs,
// because that is the step the command deliberately leaves to the operator.
func TestSetupSaysWhereItBelongsAndTouchesNothing(t *testing.T) {
	for surface, file := range map[string]string{"tmux": "~/.tmux.conf", "zsh": "~/.zshrc"} {
		_, stdout, _ := runSetup(t, []string{"status", "setup", surface})
		if !strings.Contains(stdout, file) {
			t.Errorf("%s: snippet does not name %s", surface, file)
		}
	}
}

// The generated prompt must work in zsh's default option set. PROMPT_SUBST is
// off there; relying on it renders a literal `$(cat ...)` for a user whose
// dotfiles did not happen to enable the option for another prompt framework.
func TestZshSetupRendersWithoutPromptSubst(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "torio")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf 'visible\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	snippet, err := statusSetupSnippet("zsh", shellQuote(fake))
	if err != nil {
		t.Fatal(err)
	}
	script := "unsetopt BG_NICE\n" + snippet + `
torio_status_refresh
for attempt in {1..400}; do
  cached=''
  IFS= read -r cached <"$TORIO_STATUS_CACHE" 2>/dev/null || true
  [[ "$cached" == visible ]] && break
  sleep 0.01
done
(( $+functions[torio_status_prompt] )) && torio_status_prompt
print -P -- "$RPROMPT"
`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, "-dfc", script)
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("clean zsh rejected the snippet: %v", err)
	}
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, "visible") || strings.Contains(got, "$(cat") {
		t.Fatalf("rendered prompt = %q, want the cached value without a literal command substitution", got)
	}
}

func TestSetupRefusesASurfaceItCannotConfigure(t *testing.T) {
	for _, args := range [][]string{
		{"status", "setup", "fish"},
		{"status", "setup"},
		{"status", "setup", "tmux", "zsh"},
	} {
		code, _, _ := runSetup(t, args)
		if code != int(ExitUsage) {
			t.Errorf("%v: exit = %d, want %d", args, code, ExitUsage)
		}
	}
}

// Machine mode stays machine mode: the snippet is data in the envelope rather
// than raw text beside it, so `--json` still means exactly one document.
func TestSetupUnderJSONIsAnEnvelope(t *testing.T) {
	code, stdout, stderr := runSetup(t, []string{"status", "setup", "tmux", "--json"})

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	var env struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Surface       string `json:"surface"`
			Configuration string `json:"configuration"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not the envelope: %v; got %q", err, stdout)
	}
	if !env.OK || env.Command != "status.setup" || env.Data.Surface != "tmux" {
		t.Fatalf("envelope = %+v", env)
	}
	if !strings.Contains(env.Data.Configuration, "status-right") {
		t.Errorf("configuration = %q, want the tmux snippet", env.Data.Configuration)
	}
}

// A path with a space in it is an ordinary macOS path, and both snippets hand
// theirs to a shell.
func TestShellQuote(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/usr/local/bin/torio", "/usr/local/bin/torio"},
		{"/Users/me/My Tools/torio", `'/Users/me/My Tools/torio'`},
		{"/tmp/it's/torio", `'/tmp/it'\''s/torio'`},
		{"", "''"},
	} {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
