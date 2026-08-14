package codex

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
// was not told about, so a step that starts asking something new fails here
// rather than passing on a default.
type fakeRunner struct {
	answers map[string]execx.Result
	records map[string]string
	// calls is every probe in order, so a test can assert what a step did and
	// not only what it concluded. Cleanup a step performs before failing, the
	// removal of unverified bytes for instance, is visible nowhere else.
	calls []string
	// stdin is what each ProbeInput was fed, so a test can prove a digest
	// travelled as input rather than as an argv element.
	stdin       []string
	failed      string
	remediation string
	pinned      string
	repairs     bool
	// onCall runs before each probe is answered, so a test can make the guest
	// change under a step the way a real one does when the step repairs it.
	onCall func(argv string)
}

func newFakeRunner(answers map[string]execx.Result) *fakeRunner {
	return &fakeRunner{answers: answers, records: map[string]string{}, repairs: true}
}

var errUnexpectedProbe = errors.New("unexpected probe")

func (f *fakeRunner) Probe(_ context.Context, _ string, argv ...string) (execx.Result, error) {
	joined := strings.Join(argv, " ")
	f.calls = append(f.calls, joined)
	if f.onCall != nil {
		f.onCall(joined)
	}
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

func (f *fakeRunner) ProbeInput(_ context.Context, _ string, stdin []byte, argv []string) (execx.Result, error) {
	f.stdin = append(f.stdin, string(stdin))
	return f.Probe(context.Background(), "", argv...)
}

func (f *fakeRunner) Record(name string, _ bool, detail string) { f.records[name] = detail }

func (f *fakeRunner) Fail(name, detail, remediation string) error {
	f.failed = name + ": " + detail
	// The remediation is kept separately because it is the half an operator
	// acts on, and a test that only reads the detail cannot tell a failure that
	// says what to do next from one that leaves them stuck.
	f.remediation = remediation
	return errors.New(f.failed)
}

func (f *fakeRunner) PinnedVersion() string { return f.pinned }

func (f *fakeRunner) Reconcile() bool { return f.repairs }

// Compile-time proof the fake really is the interface a backend is handed.
var _ backend.StepRunner = (*fakeRunner)(nil)

func out(s string) execx.Result { return execx.Result{Stdout: []byte(s)} }
func exit(c int) execx.Result   { return execx.Result{ExitCode: c} }

// TestNoSudoAcceptsOnlyTheDocumentedNoAnswer is the check this backend's custody
// claim rests on. It is the Claude Code check retold for this identity, and it
// is repeated rather than shared for the same reason the parsers are: the
// sentences it matches are what a guest actually printed, and a helper that grew
// a lenient mode for one backend would quietly change what the other proves.
func TestNoSudoAcceptsOnlyTheDocumentedNoAnswer(t *testing.T) {
	const probe = "env LC_ALL=C sudo -n -l -U " + User
	const denied = "User codex is not allowed to run sudo on lima-torio-ci-codex.\n"
	const granted = "Matching Defaults entries for codex on lima-torio-ci-codex:\n    env_reset\n\n" +
		"User codex may run the following commands on lima-torio-ci-codex:\n    (ALL) NOPASSWD: ALL\n"

	t.Run("the denial sentence is the pass", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out(denied)})
		if err := verifyNoSudo(context.Background(), r); err != nil {
			t.Fatalf("verifyNoSudo: %v", err)
		}
		if got := r.records["codex_no_sudo"]; got == "" {
			t.Error("a passing check recorded nothing")
		}
	})

	t.Run("a grant fails even though sudo exited 0", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out(granted)})
		if err := verifyNoSudo(context.Background(), r); err == nil {
			t.Fatal("an identity that may run sudo commands passed the check")
		}
	})

	t.Run("an unrecognized answer fails closed", func(t *testing.T) {
		for _, answer := range []string{"", "sudo: unknown user codex\n", "some future phrasing\n"} {
			r := newFakeRunner(map[string]execx.Result{probe: out(answer)})
			if err := verifyNoSudo(context.Background(), r); err == nil {
				t.Fatalf("%q was read as proof of no sudo", answer)
			}
		}
	})

	for _, code := range []int{1, 2, 127, 255} {
		t.Run("an unanswerable question fails closed", func(t *testing.T) {
			r := newFakeRunner(map[string]execx.Result{probe: exit(code)})
			if err := verifyNoSudo(context.Background(), r); err == nil {
				t.Fatalf("exit %d was read as proof of no sudo", code)
			}
		})
	}
}

// TestGroupsExactRefusesAnythingUnaccountedFor pins that the group check is a
// closed list. An interactive session runs as this uid, so every group it holds
// is a group the agent holds.
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
// absent credential is the expected state of a fresh box.
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
			if got := r.records[authCheck]; !strings.Contains(got, tc.want) {
				t.Errorf("recorded %q, want it to say %q", got, tc.want)
			}
		})
	}
}
