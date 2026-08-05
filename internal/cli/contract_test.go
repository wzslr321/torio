package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestUnknownGlobalFlagStillRejected keeps the fail-closed flag contract: a
// genuinely unknown persistent flag remains a usage error even after D2 adds
// --config as a real global (see config_test.go).
func TestUnknownGlobalFlagStillRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "--not-a-flag", "/tmp/x"}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Errorf("unknown flag: exit = %d, want %d", code, int(ExitUsage))
	}
}

// TestStateDirFlagIsGone pins the removal rather than leaving it to the
// unknown-flag rule by accident: --state-dir was an accepted global, and a
// document of that change is worth more than the absence of a registration.
// Torio writes no host state, so there is no directory to point it at
// (ADR-0001).
func TestStateDirFlagIsGone(t *testing.T) {
	for _, args := range [][]string{
		{"--state-dir", "/tmp/x", "version"},
		{"version", "--state-dir", "/tmp/x"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), args, &stdout, &stderr, testBuild())
		if code != int(ExitUsage) {
			t.Errorf("%v: exit = %d, want %d", args, code, int(ExitUsage))
		}
	}
}

// TestHelpIsTheNarrowExceptionToJSON documents and locks the one exception to
// the --json single-envelope invariant: --help is a human-only affordance that
// prints usage text to stdout and exits 0, even when --json is present. It must
// NOT emit an envelope, and it must not be treated as a JSON document.
func TestHelpIsTheNarrowExceptionToJSON(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "--help"},
		{"version", "--json", "--help"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), args, &stdout, &stderr, testBuild())
		if code != int(ExitOK) {
			t.Fatalf("%v: exit = %d, want 0; stderr=%q", args, code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "Usage:") {
			t.Errorf("%v: expected human help text with 'Usage:', got %q", args, out)
		}
		// Must not be our JSON envelope.
		var env map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &env); err == nil {
			if _, isEnvelope := env["schema_version"]; isEnvelope {
				t.Errorf("%v: --help produced a JSON envelope; help must be the human-only exception", args)
			}
		}
	}
}

// TestHelpTextCarriesNoProductVersionLabel keeps release labels out of every
// string an operator can read. Which Torio scope is running is answered by
// `torio version` and nowhere else: help text that says "V1" tells the reader
// nothing they can act on, and it ages into a falsehood the moment the next
// scope lands. Go comments are deliberately exempt — they are not a user-facing
// surface, and they carry the ADR context that explains why a rule exists.
func TestHelpTextCarriesNoProductVersionLabel(t *testing.T) {
	label := regexp.MustCompile(`(?i)\bv[0-9]+\b`)

	check := func(where, field, text string) {
		if m := label.FindString(text); m != "" {
			t.Errorf("%s: %s carries the product version label %q; "+
				"the operator reads the version from `torio version`", where, field, m)
		}
	}

	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		where := c.CommandPath()
		check(where, "Short", c.Short)
		check(where, "Long", c.Long)
		check(where, "Example", c.Example)
		visit := func(f *pflag.Flag) { check(where, "flag --"+f.Name, f.Usage) }
		c.Flags().VisitAll(visit)
		c.PersistentFlags().VisitAll(visit)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newRootCmd(&app{stdout: io.Discard, stderr: io.Discard, build: testBuild()}))
}
