package sshagent

import (
	"strings"
	"testing"
)

// TestWithPromptMessageLeavesExactlyOneAssignment is the regression for a
// variable of this name already exported in the operator's shell.
//
// Appending after an inherited assignment leaves two, and which one a program
// resolves to is not a thing to rely on. Here the answer decides what the
// operator reads before approving a signature, so the inherited one must be
// gone rather than merely outranked.
func TestWithPromptMessageLeavesExactlyOneAssignment(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		promptEnvVar + "=left over from testing the dialog by hand",
		"TERM=xterm",
		promptEnvVar + "=and another",
	}

	got := withPromptMessage(env, "the real message")

	var assignments []string
	for _, kv := range got {
		if strings.HasPrefix(kv, promptEnvVar+"=") {
			assignments = append(assignments, kv)
		}
	}
	if len(assignments) != 1 {
		t.Fatalf("environment carries %d assignments of %s, want exactly one: %v", len(assignments), promptEnvVar, assignments)
	}
	if assignments[0] != promptEnvVar+"=the real message" {
		t.Errorf("assignment = %q, want the message this call passed", assignments[0])
	}
	for _, want := range []string{"PATH=/usr/bin", "TERM=xterm"} {
		if !slicesContains(got, want) {
			t.Errorf("environment lost %q; only the prompt variable may be replaced", want)
		}
	}
}

func TestWithPromptMessageAddsOneWhenNoneWasSet(t *testing.T) {
	got := withPromptMessage([]string{"PATH=/usr/bin"}, "message")
	if len(got) != 2 || got[1] != promptEnvVar+"=message" {
		t.Errorf("withPromptMessage() = %v, want the original plus one assignment", got)
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
