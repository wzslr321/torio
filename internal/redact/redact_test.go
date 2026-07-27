package redact

import (
	"regexp"
	"strings"
	"testing"
)

// canaries are representative fake secret shapes. They are NOT real
// credentials. Failure messages below never echo these values.
var canaries = map[string]string{
	"openai":      "sk-ABCDEFGHIJKLMNOPQRSTUVWXYZ123456",
	"github":      "ghp_0123456789abcdefghij",
	"slack":       "xoxb-0123456789abcdef",
	"aws":         "AKIA0123456789ABCDEF",
	"private-key": "-----BEGIN OPENSSH PRIVATE KEY-----",
}

// TestCanariesMatchProductionMatchers pins each positive fixture to the
// production matcher it is meant to exercise and asserts the match, so a later
// abbreviation or typo (e.g. "sk-ABC...7890") fails here loudly instead of
// silently reducing every redaction canary below to a no-op that would "pass"
// against unredacted output. This is the fixture-validity gate the redaction
// canaries depend on.
func TestCanariesMatchProductionMatchers(t *testing.T) {
	want := map[string]*regexp.Regexp{
		"openai":      patterns[0],
		"github":      patterns[1],
		"slack":       patterns[2],
		"aws":         patterns[3],
		"private-key": patterns[4],
	}
	if len(want) != len(canaries) {
		t.Fatalf("pinned matchers (%d) and canaries (%d) are out of sync", len(want), len(canaries))
	}
	for name, secret := range canaries {
		p, ok := want[name]
		if !ok {
			t.Fatalf("canary %q has no pinned production matcher", name)
		}
		if !p.MatchString(secret) {
			// Report by name and pattern only; never echo the fixture value.
			t.Errorf("canary %q does not match its intended production matcher %s", name, p.String())
		}
	}
}

func TestStringRedactsKnownSecretShapes(t *testing.T) {
	for name, secret := range canaries {
		in := "context before " + secret + " context after"
		out := String(in)
		if strings.Contains(out, secret) {
			// Do not print the leaked secret; report by name only.
			t.Errorf("canary %q was not redacted", name)
		}
		if !strings.Contains(out, Placeholder) {
			t.Errorf("canary %q: output missing %q placeholder", name, Placeholder)
		}
	}
}

func TestStringLeavesNonSecretsUnchanged(t *testing.T) {
	in := "torio version 1.2.3 (commit abcdef0) go1.26.5 darwin/arm64"
	if out := String(in); out != in {
		t.Errorf("non-secret text was altered: %q -> %q", in, out)
	}
}

func TestRedactorMasksRegisteredLiterals(t *testing.T) {
	const secret = "correct-horse-battery-staple-9f3a"
	r := New(secret)
	out := r.String("value is " + secret + " end")
	if strings.Contains(out, secret) {
		t.Errorf("registered literal secret was not masked")
	}
	if !strings.Contains(out, Placeholder) {
		t.Errorf("output missing placeholder: %q", out)
	}
}

// TestRedactorHandlesOverlappingLiterals is the regression for the prefix bug:
// a shorter literal that is an abbreviation/prefix of a longer one must not
// leave a residue of the longer secret (New("abc","abcdef") must never leave
// "def"). Order of registration must not matter.
func TestRedactorHandlesOverlappingLiterals(t *testing.T) {
	for _, lits := range [][]string{{"abc", "abcdef"}, {"abcdef", "abc"}} {
		r := New(lits...)
		out := r.String("x abcdef y")
		if strings.Contains(out, "def") {
			t.Errorf("literals %v left residue: %q", lits, out)
		}
		if strings.Contains(out, "abc") {
			t.Errorf("literals %v left residue: %q", lits, out)
		}
		if !strings.Contains(out, Placeholder) {
			t.Errorf("literals %v: missing placeholder: %q", lits, out)
		}
	}
}

func TestRedactorIgnoresEmptyLiteral(t *testing.T) {
	// An empty registered secret must not turn every gap into a placeholder.
	r := New("")
	in := "nothing to redact here"
	if out := r.String(in); out != in {
		t.Errorf("empty literal altered output: %q -> %q", in, out)
	}
}

func TestSliceRedactsEachElement(t *testing.T) {
	in := []string{"--token", canaries["github"], "--user", "alice"}
	out := Slice(in)
	if len(out) != len(in) {
		t.Fatalf("Slice changed length: got %d, want %d", len(out), len(in))
	}
	if strings.Contains(out[1], canaries["github"]) {
		t.Errorf("secret element was not redacted")
	}
	if out[3] != "alice" {
		t.Errorf("non-secret element altered: %q", out[3])
	}
	// Input must not be mutated in place.
	if in[1] != canaries["github"] {
		t.Errorf("Slice mutated its input")
	}
}
