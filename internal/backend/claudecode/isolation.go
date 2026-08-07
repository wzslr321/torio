package claudecode

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/lima"
)

// VerifyIsolation proves the identity holds no authority beyond its own work.
//
// It is stricter than the Hermes equivalent, and the extra strictness is the
// point of this backend's custody story rather than decoration. An interactive
// agent session runs as this uid: whatever it can reach, the agent can reach,
// for as long as it is running. So the check does not merely exclude the one
// group that would be catastrophic — it enumerates the whole group set and
// refuses anything that is not accounted for, and it proves the absence of
// sudo rather than assuming it.
func (claudeBackend) VerifyIsolation(ctx context.Context, r backend.StepRunner) error {
	if err := verifyGroupsExact(ctx, r); err != nil {
		return err
	}
	return verifyNoSudo(ctx, r)
}

// allowedGroups is the complete supplementary group set for the identity: its
// own primary group, and the shared workspace group. Nothing else has a reason
// to be there, so anything else is drift.
//
// When the MCP broker returns (issue #2), its client group joins this list —
// deliberately as an edit here rather than as a rule that admits unknown
// groups, because a check that tolerates additions is not this check.
func allowedGroups() []string { return []string{User, lima.TorioProjectsGroup} }

func verifyGroupsExact(ctx context.Context, r backend.StepRunner) error {
	const name = "claude_groups_exact"
	res, err := r.Probe(ctx, name, "id", "-nG", User)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return r.Fail(name, "cannot read claude group membership", "confirm the claude user exists on the guest")
	}
	got := strings.Fields(string(res.Stdout))
	allowed := allowedGroups()
	var unexpected []string
	for _, g := range got {
		if !slices.Contains(allowed, g) {
			unexpected = append(unexpected, g)
		}
	}
	if len(unexpected) > 0 {
		slices.Sort(unexpected)
		return r.Fail(name,
			fmt.Sprintf("claude holds %d group(s) outside the declared set: %s", len(unexpected), strings.Join(unexpected, " ")),
			"remove the extra group membership; the agent identity holds exactly its own group and "+lima.TorioProjectsGroup)
	}
	for _, want := range allowed {
		if !slices.Contains(got, want) {
			return r.Fail(name, "claude is not in "+want, "add claude to "+want+" on the guest")
		}
	}
	r.Record(name, true, strings.Join(allowed, " "))
	return nil
}

// verifyNoSudo proves the identity cannot become root.
//
// `sudo -n -l -U <user>` must exit exactly 1 — the documented "may run no
// commands" answer. Exit 0 means it may run something, which fails. Every other
// exit means the question could not be asked, which also fails: a check that
// treats "I could not tell" as "no" reports OK precisely when it cannot see,
// and one sudoers change would turn it green on a guest where nothing holds.
func verifyNoSudo(ctx context.Context, r backend.StepRunner) error {
	const name = "claude_no_sudo"
	res, err := r.Probe(ctx, name, "sudo", "-n", "-l", "-U", User)
	if err != nil {
		return err
	}
	switch res.ExitCode {
	case 1:
		r.Record(name, true, "may run no sudo commands")
		return nil
	case 0:
		return r.Fail(name, "claude may run commands through sudo",
			"remove the sudoers grant; an agent identity that can become root is above every control the guest enforces")
	default:
		return r.Fail(name, fmt.Sprintf("could not determine claude's sudo rights (exit %d)", res.ExitCode),
			"confirm sudo is present on the guest and re-run bootstrap; an unanswerable question is not a no")
	}
}
