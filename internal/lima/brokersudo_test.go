package lima

import (
	"testing"
)

// TestSudoVerdictReadsTheAnswerAndNotTheExitCode is the whole of this fix.
//
// Asked by a caller that already holds root, `sudo -l -U <user>` exits 0 whether
// the user may run everything or nothing. Measured on a Lima guest running
// Ubuntu 24.04 with sudo 1.9.15p5: the identity with no sudo at all and the
// identity with all of it both exit 0. A check keyed on that exit code cannot
// tell them apart, and the one it reported was the wrong one.
func TestSudoVerdictReadsTheAnswerAndNotTheExitCode(t *testing.T) {
	const denied = "User torio-mcp is not allowed to run sudo on lima-torio-ci-codex-local.\n"
	const granted = "Matching Defaults entries for torio-mcp on lima-torio-ci-codex-local:\n    env_reset\n\n" +
		"User torio-mcp may run the following commands on lima-torio-ci-codex-local:\n    (ALL) NOPASSWD: ALL\n"

	// The exit code is 0 in every one of these, which is the point: it is the
	// same 0 the old check read as proof of a grant.
	t.Run("the denial sentence is the pass", func(t *testing.T) {
		if got := sudoVerdict(result{exit: 0, out: denied}); got != sudoAbsent {
			t.Fatalf("verdict %v on the sentence a guest prints for an identity with no sudo", got)
		}
	})

	t.Run("the grant sentence fails", func(t *testing.T) {
		if got := sudoVerdict(result{exit: 0, out: granted}); got != sudoPresent {
			t.Fatalf("verdict %v on an identity that may run everything", got)
		}
	})

	// Silence is what a truncated, redirected or reworded sudo produces, and it
	// is not a denial. Inferring "no sudo" from the absence of a grant is how a
	// custody proof comes to pass on a guest it can no longer see.
	t.Run("an unrecognized answer is unprovable, not a no", func(t *testing.T) {
		for _, out := range []string{"", "sudo: unknown user torio-mcp\n", "some future phrasing\n"} {
			if got := sudoVerdict(result{exit: 0, out: out}); got != sudoUnprovable {
				t.Errorf("%q produced verdict %v, want it to be unprovable", out, got)
			}
		}
	})

	t.Run("a question that was not answered is unprovable", func(t *testing.T) {
		for _, exit := range []int{1, 2, 127, 255} {
			if got := sudoVerdict(result{exit: exit, out: denied}); got != sudoUnprovable {
				t.Errorf("exit %d produced verdict %v, want it to be unprovable", exit, got)
			}
		}
	})
}

// sudoDeniedFixture is what a guest prints for an identity that holds no sudo,
// asked by a caller that already holds root. It is a transcript rather than a
// convenient shape: the fixtures it replaces fed a bare exit code, which is a
// thing sudo does not produce for this question, and every check reading them
// was green against a guest that cannot exist.
const sudoDeniedFixture = "User torio-mcp is not allowed to run sudo on lima-guest.\n"
