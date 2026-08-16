//go:build platform_e2e

package platform

import (
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The journey runs against one backend per invocation, selected by
// PLATFORM_E2E_BACKEND and defaulting to the one Torio shipped first.
//
// One journey parameterized by backend, rather than two journeys, is deliberate.
// A second copy of the harness would drift from the first — and the drift would
// land exactly in the shared parts, the ones that are supposed to be identical
// on every backend and are therefore the whole point of having a contract. What
// differs between backends belongs in the small per-backend blocks below; what
// does not differ is asserted once, for every backend that ships.
const (
	backendClaudeCode = "claude-code"
	backendCodex      = "codex"
)

func journeyBackend() string {
	if b := os.Getenv("PLATFORM_E2E_BACKEND"); b != "" {
		return b
	}
	return backendClaudeCode
}

// backendProfile is what the journey needs to know about the backend under
// test: who owns the guest, where its things are, and which capabilities it
// declares — because a capability a backend declares it has not got is exactly
// what must be asserted as absent rather than skipped in silence.
type backendProfile struct {
	name string
	// user is the guest identity that owns checkouts and the vault.
	user string
	// workspace and vault are its two trees.
	workspace string
	vault     string
	// versionCheck is the bootstrap check whose detail carries the version, and
	// versionCommand is the documented stable command path an operator is told
	// to use. Both are per-backend because both are the backend's own.
	versionCheck   string
	versionCommand []string
	// skillFile is where this backend discovers the Brain retrieval skill. It is
	// asserted as a real file owned by the agent identity, because "installed"
	// in a report and "present where this agent looks" are different claims —
	// and the second is the one that decides whether the vault is reachable.
	skillFile string
	// declaresRegistry, declaresService and declaresSession are the three
	// capabilities. A false one is asserted, not skipped: the contract's claim
	// is that Torio reports an absent capability as a state, and a journey that
	// simply did not look would not be testing that.
	declaresRegistry bool
	declaresService  bool
	declaresSession  bool
	// declaresWaitingMarker is the fourth, and it is separate because a backend
	// can run a session process without having any way to say that the session is
	// waiting on a human. Where it is true the journey drives the hook helper and
	// reads the answer back out of `torio status`.
	declaresWaitingMarker bool
	// requiredChecks are bootstrap checks that must be present and OK. They are
	// the backend's own custody proofs, the ones no other backend has.
	requiredChecks []string
}

func profileFor(name string) backendProfile {
	switch name {
	case backendClaudeCode:
		return backendProfile{
			name:                  backendClaudeCode,
			user:                  "claude",
			workspace:             "/home/claude/projects",
			vault:                 "/home/claude/brain",
			versionCheck:          "claude_version",
			versionCommand:        []string{"sudo", "-u", "claude", "--", "claude", "--version"},
			skillFile:             "/home/claude/.claude/skills/torio-brain/SKILL.md",
			declaresRegistry:      false,
			declaresService:       false,
			declaresSession:       true,
			declaresWaitingMarker: true,
			// The four proofs this backend's custody rests on: the identity
			// exists, it cannot become root, it holds nothing beyond its own
			// work, and the binary it runs is one it cannot rewrite.
			requiredChecks: []string{
				"claude_user",
				"claude_no_sudo",
				"claude_groups_exact",
				"claude_install",
				"claude_managed_settings",
				"claude_waiting_marker_dependencies",
				"claude_waiting_marker_helper",
				"claude_waiting_marker_state",
				"agent_session_helper",
			},
		}
	case backendCodex:
		return backendProfile{
			name:                  backendCodex,
			user:                  "codex",
			workspace:             "/home/codex/projects",
			vault:                 "/home/codex/brain",
			versionCheck:          "codex_version",
			versionCommand:        []string{"sudo", "-u", "codex", "--", "codex", "--version"},
			skillFile:             "/home/codex/.codex/skills/torio-brain/SKILL.md",
			declaresRegistry:      false,
			declaresService:       false,
			declaresSession:       true,
			declaresWaitingMarker: true,
			// The same custody proofs the other process backend rests on, plus
			// the system configuration layer this one puts its guardrails in.
			requiredChecks: []string{
				"codex_user",
				"codex_no_sudo",
				"codex_groups_exact",
				"codex_install",
				"codex_system_config",
				"codex_waiting_marker_dependencies",
				"codex_waiting_marker_helper",
				"codex_waiting_marker_state",
				"agent_session_helper",
			},
		}
	}
	// An unknown name is a broken invocation, not a reason to fall back. A
	// default case here made a typo in a CI matrix run the first backend's
	// journey twice and report both legs green — the one outcome a two-backend
	// gate exists to make impossible.
	Fail(fmt.Sprintf("unknown PLATFORM_E2E_BACKEND %q; known backends are %q and %q",
		name, backendClaudeCode, backendCodex))
	return backendProfile{}
}

// expectChecksOK asserts that every named bootstrap check is present in the
// report and passed.
//
// Presence is half the assertion and the more important half. A backend's
// custody proofs are exactly the checks no other backend has, so a run that
// silently stopped performing one would otherwise look identical to a run where
// it passed — and the report would say the guest was verified.
func expectChecksOK(rep envelope, names []string) {
	GinkgoHelper()
	checks, ok := rep.Data["checks"].([]any)
	Expect(ok).To(BeTrue(), "bootstrap data carries no checks array")

	seen := map[string]bool{}
	for _, raw := range checks {
		c, isMap := raw.(map[string]any)
		if !isMap {
			continue
		}
		name, _ := c["name"].(string)
		okFlag, _ := c["ok"].(bool)
		seen[name] = okFlag
	}
	for _, want := range names {
		state, present := seen[want]
		Expect(present).To(BeTrue(), "bootstrap performed no %q check; an absent proof is not a passing one", want)
		Expect(state).To(BeTrue(), "bootstrap check %q did not pass", want)
	}
}
