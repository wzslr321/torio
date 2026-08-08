package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

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
