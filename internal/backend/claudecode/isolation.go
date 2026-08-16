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
// The strictness is the
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

// allowedGroups is the closed group set. The broker client group is optional
// because bootstrap must pass both before and after the operator installs MCP;
// it conveys only permission to connect to policy-enforcing sockets.
func allowedGroups() []string {
	return []string{User, lima.TorioProjectsGroup, lima.TorioMCPClientsGroup}
}

func requiredGroups() []string { return []string{User, lima.TorioProjectsGroup} }

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
			"remove the extra group membership; the only optional group is "+lima.TorioMCPClientsGroup)
	}
	for _, want := range requiredGroups() {
		if !slices.Contains(got, want) {
			return r.Fail(name, "claude is not in "+want, "add claude to "+want+" on the guest")
		}
	}
	slices.Sort(got)
	r.Record(name, true, strings.Join(got, " "))
	return nil
}

// The two answers `sudo -l -U` gives, in the C locale. They are matched rather
// than one being inferred from the absence of the other: an unrecognized answer
// must fail, and "neither phrase appeared" is not a proof of anything.
const (
	sudoDeniedPhrase  = "is not allowed to run sudo"
	sudoGrantedPhrase = "may run the following commands"
)

// verifyNoSudo proves the identity cannot become root.
//
// The question is asked about the user, by a caller that already holds root:
// `sudo -n -l -U <user>`. Asking it *as* the user instead looks cleaner and is
// wrong, which a real guest is what showed. As claude, `sudo -n -l` exits 1
// saying "a password is required" — the same exit an identity with
// password-gated sudo produces. Treating that 1 as proof of no sudo would
// report OK for exactly the identity that can become root by typing a password.
//
// The answer is in the output, not the exit code. Asked by root, sudo 1.9.15
// exits 0 whether the user may run everything or nothing, and says which. So
// this matches the denial sentence positively, fails on the grant sentence, and
// fails on anything else — including a non-zero exit, which here means the
// question was not answered rather than answered "no". A check that reads "I
// could not tell" as "no" reports OK precisely when it can see nothing.
func verifyNoSudo(ctx context.Context, r backend.StepRunner) error {
	const name = "claude_no_sudo"
	// LC_ALL=C pins the two sentences this reads. Without it the guest's locale
	// decides whether a custody proof parses.
	res, err := r.Probe(ctx, name, "env", "LC_ALL=C", "sudo", "-n", "-l", "-U", User)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return r.Fail(name, fmt.Sprintf("could not determine claude's sudo rights (exit %d)", res.ExitCode),
			"confirm sudo is present on the guest and re-run bootstrap; an unanswerable question is not a no")
	}
	answer := string(res.Stdout)
	switch {
	case strings.Contains(answer, sudoDeniedPhrase):
		r.Record(name, true, "may run no sudo commands")
		return nil
	case strings.Contains(answer, sudoGrantedPhrase):
		return r.Fail(name, "claude may run commands through sudo",
			"remove the sudoers grant; an agent identity that can become root is above every control the guest enforces")
	default:
		return r.Fail(name, "could not determine claude's sudo rights (unrecognized sudo output)",
			"confirm sudo is present on the guest and re-run bootstrap; an unanswerable question is not a no")
	}
}
