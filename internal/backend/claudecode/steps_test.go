package claudecode

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/execx"
)

// fakeRunner is a bootstrap run the tests drive. It answers probes by matching
// the joined argv, records what was checked, and refuses to answer a probe it
// was not told about — so a step that starts asking something new fails here
// rather than silently passing on a default.
type fakeRunner struct {
	answers map[string]execx.Result
	records map[string]string
	// calls is every probe in order, so a test can assert what a step did and
	// not only what it concluded. Cleanup a step performs before failing — the
	// removal of unverified bytes, say — is visible nowhere else.
	calls  []string
	failed string
}

func newFakeRunner(answers map[string]execx.Result) *fakeRunner {
	return &fakeRunner{answers: answers, records: map[string]string{}}
}

var errUnexpectedProbe = errors.New("unexpected probe")

func (f *fakeRunner) Probe(_ context.Context, _ string, argv ...string) (execx.Result, error) {
	joined := strings.Join(argv, " ")
	f.calls = append(f.calls, joined)
	res, ok := f.answers[joined]
	if !ok {
		return execx.Result{}, errUnexpectedProbe
	}
	return res, nil
}

// saw reports whether any probe contained sub.
func (f *fakeRunner) saw(sub string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func (f *fakeRunner) ProbeInput(_ context.Context, _ string, _ []byte, argv []string) (execx.Result, error) {
	return f.Probe(context.Background(), "", argv...)
}

func (f *fakeRunner) Record(name string, _ bool, detail string) { f.records[name] = detail }

func (f *fakeRunner) Fail(name, detail, _ string) error {
	f.failed = name + ": " + detail
	return errors.New(f.failed)
}

func (f *fakeRunner) PinnedVersion() string { return "" }

// The fake reconciles, so every step under test takes its repairing path and
// the assertions below are about what that path does.
func (f *fakeRunner) Reconcile() bool { return true }

// Compile-time proof the fake really is the interface a backend is handed.
var _ backend.StepRunner = (*fakeRunner)(nil)

func out(s string) execx.Result { return execx.Result{Stdout: []byte(s)} }
func exit(c int) execx.Result   { return execx.Result{ExitCode: c} }

// TestNoSudoAcceptsOnlyTheDocumentedNoAnswer is the check this backend's whole
// custody claim rests on, and the one place where "I could not tell" must not
// read as "no".
//
// The fixtures are transcripts from a real guest, sudo 1.9.15p5 on Ubuntu
// 24.04, because the earlier version of this check was written against the exit
// code and the exit code does not carry the answer: asked by root, `sudo -l -U`
// exits 0 whether the user may run everything or nothing. A check keyed on
// exit 1 could therefore never pass, and the whole backend could never
// bootstrap.
func TestNoSudoAcceptsOnlyTheDocumentedNoAnswer(t *testing.T) {
	const probe = "env LC_ALL=C sudo -n -l -U " + User
	const denied = "User claude is not allowed to run sudo on lima-torio-ci-claude.\n"
	const granted = "Matching Defaults entries for claude on lima-torio-ci-claude:\n    env_reset\n\n" +
		"User claude may run the following commands on lima-torio-ci-claude:\n    (ALL) NOPASSWD: ALL\n"

	t.Run("the denial sentence is the pass", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out(denied)})
		if err := verifyNoSudo(context.Background(), r); err != nil {
			t.Fatalf("verifyNoSudo: %v", err)
		}
		if got := r.records["claude_no_sudo"]; got == "" {
			t.Error("a passing check recorded nothing")
		}
	})

	t.Run("a grant fails even though sudo exited 0", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out(granted)})
		if err := verifyNoSudo(context.Background(), r); err == nil {
			t.Fatal("an identity that may run sudo commands passed the check")
		}
		if !strings.Contains(r.failed, "sudo") {
			t.Errorf("failure does not name the problem: %q", r.failed)
		}
	})

	// Silence is the case that matters most. It is what a truncated, redirected
	// or reworded sudo produces, and it is not a denial — inferring "no sudo"
	// from the absence of a grant is how this check would come to pass on a
	// guest it can no longer see.
	t.Run("an unrecognized answer fails closed", func(t *testing.T) {
		for _, answer := range []string{"", "sudo: unknown user claude\n", "some future phrasing\n"} {
			r := newFakeRunner(map[string]execx.Result{probe: out(answer)})
			if err := verifyNoSudo(context.Background(), r); err == nil {
				t.Fatalf("%q was read as proof of no sudo", answer)
			}
		}
	})

	for _, code := range []int{1, 2, 127, 255} {
		t.Run("an unanswerable question fails closed", func(t *testing.T) {
			// Exit 1 is in this list deliberately. Asked as the identity itself
			// rather than about it, sudo exits 1 saying "a password is required"
			// — the same 1 a password-gated grant produces, which is exactly the
			// identity this check exists to catch.
			r := newFakeRunner(map[string]execx.Result{probe: exit(code)})
			if err := verifyNoSudo(context.Background(), r); err == nil {
				t.Fatalf("exit %d was read as proof of no sudo", code)
			}
		})
	}
}

// TestGroupsExactRefusesAnythingUnaccountedFor pins that the group check is a
// closed list rather than an exclusion of the one group that would be
// catastrophic. An interactive session runs as this uid, so every group it
// holds is a group the agent holds.
func TestGroupsExactRefusesAnythingUnaccountedFor(t *testing.T) {
	const probe = "id -nG " + User

	t.Run("exactly the declared set passes", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out(User + " torio-projects\n")})
		if err := verifyGroupsExact(context.Background(), r); err != nil {
			t.Fatalf("verifyGroupsExact: %v", err)
		}
	})

	t.Run("the broker client group is the one optional declared addition", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out(User + " torio-projects torio-mcp-clients\n")})
		if err := verifyGroupsExact(context.Background(), r); err != nil {
			t.Fatalf("verifyGroupsExact after mcp install: %v", err)
		}
	})

	t.Run("an extra group fails and is named", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out(User + " torio-projects docker\n")})
		if err := verifyGroupsExact(context.Background(), r); err == nil {
			t.Fatal("an identity in an undeclared group passed the check")
		}
		if !strings.Contains(r.failed, "docker") {
			t.Errorf("failure does not name the unexpected group: %q", r.failed)
		}
	})

	t.Run("a missing required group fails", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out(User + "\n")})
		if err := verifyGroupsExact(context.Background(), r); err == nil {
			t.Fatal("an identity outside the shared workspace group passed the check")
		}
	})
}

// TestProbeAuthNeverFailsABootstrap pins the ordering constraint that makes the
// box usable: a guest has to bootstrap before anyone can log in to it, so an
// absent credential is the expected state of a fresh box and must be reported
// rather than treated as a defect.
func TestProbeAuthNeverFailsABootstrap(t *testing.T) {
	const probe = "sudo -n -u " + User + " -- test -s " + credentialPath

	for _, tc := range []struct {
		name   string
		result execx.Result
		want   string
	}{
		{"present", exit(0), "present"},
		{"absent", exit(1), "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFakeRunner(map[string]execx.Result{probe: tc.result})
			if err := New().ProbeAuth(context.Background(), r); err != nil {
				t.Fatalf("ProbeAuth failed a bootstrap over a credential: %v", err)
			}
			if got := r.records["claude_auth"]; !strings.Contains(got, tc.want) {
				t.Errorf("recorded %q, want it to say %q", got, tc.want)
			}
		})
	}
}

// TestMCPReportingNeverFailsAndNamesOnly pins the compensating legibility for
// the hole this backend accepts. The source is a file the agent owns, so the
// check cannot be a boundary; what it must do is report, never fail, and never
// carry a value that could be a token.
func TestMCPReportingNeverFailsAndNamesOnly(t *testing.T) {
	const present = "sudo -n -u " + User + " -- test -f " + mcpConfigPath

	t.Run("no configuration is a state, not a failure", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{present: exit(1)})
		if err := reportMCPServers(context.Background(), r); err != nil {
			t.Fatalf("reportMCPServers: %v", err)
		}
		if got := r.records["claude_mcp_servers"]; !strings.Contains(got, "none") {
			t.Errorf("recorded %q, want it to say none configured", got)
		}
	})

	t.Run("configured servers are reported as unverified", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{
			present: exit(0),
			"sudo -n -u " + User + " -- python3 -c " + mcpNamesProgram + " " + mcpConfigPath: out("atlassian\nslack\n"),
		})
		if err := reportMCPServers(context.Background(), r); err != nil {
			t.Fatalf("reportMCPServers: %v", err)
		}
		got := r.records["claude_mcp_servers"]
		for _, want := range []string{"atlassian", "slack", "not verified"} {
			if !strings.Contains(got, want) {
				t.Errorf("recorded %q, want it to contain %q", got, want)
			}
		}
	})

	t.Run("an unreadable agent-owned document is not a failure", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{
			present: exit(0),
			"sudo -n -u " + User + " -- python3 -c " + mcpNamesProgram + " " + mcpConfigPath: exit(1),
		})
		if err := reportMCPServers(context.Background(), r); err != nil {
			t.Fatalf("reportMCPServers failed over a file the agent owns: %v", err)
		}
	})
}
