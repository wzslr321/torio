package cli

import (
	"bytes"
	"context"

	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/backend/claudecode"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

// statusBackend is a backend that declares exactly the check names a test wants
// to see read back. It exists because the defect these tests pin was not in any
// probe: every probe ran and recorded the truth, and the renderer looked
// somewhere else.
type statusBackend struct {
	backend.Backend
	name   string
	checks backend.StatusChecks
}

func (b statusBackend) Identity() backend.Identity {
	return backend.Identity{Name: b.name, GuestUser: "agent"}
}
func (b statusBackend) StatusChecks() backend.StatusChecks { return b.checks }
func (statusBackend) Registry() backend.ProjectRegistry    { return nil }
func (statusBackend) Service() *backend.ServiceSpec        { return nil }
func (statusBackend) Session() *backend.SessionSpec        { return &backend.SessionSpec{} }

func statusReport(checks ...lima.CheckResult) lima.BootstrapReport {
	return lima.BootstrapReport{Checks: checks}
}

func renderStatus(t *testing.T, b backend.Backend, rep lima.BootstrapReport) string {
	t.Helper()
	var out bytes.Buffer
	a := &app{stdout: &out, stderr: &bytes.Buffer{}, build: testBuild(), backend: b, jsonOut: true}
	if err := a.emitBackendStatus(rep); err != nil {
		t.Fatalf("emitBackendStatus: %v", err)
	}
	return out.String()
}

// TestStatusReadsTheCheckNamesTheBackendDeclares is the regression, and it is
// worth stating what it cost. The renderer built its lookup key by appending
// "_auth" to the name the backend is registered under. For Hermes that happened
// to be the name of the check; for Claude Code, registered as `claude-code` and
// recording `claude_auth`, it matched nothing. So a box that had proven it held
// a credential reported not-applicable — the answer that means Torio has no way
// to ask — and the operator was never told to log in.
func TestStatusReadsTheCheckNamesTheBackendDeclares(t *testing.T) {
	b := statusBackend{
		name:   "claude-code",
		checks: backend.StatusChecks{Version: "claude_version", Auth: "claude_auth", MCPServers: "claude_mcp_servers"},
	}
	rep := statusReport(
		lima.CheckResult{Name: "claude_version", OK: true, Detail: "2.1.220"},
		lima.CheckResult{Name: "claude_auth", OK: true, Detail: backend.CredentialPresent},
		lima.CheckResult{Name: "claude_mcp_servers", OK: true, Detail: "none configured"},
	)
	got := renderStatus(t, b, rep)
	for _, want := range []string{
		`"credentials":"present"`,
		`"version":"2.1.220"`,
		`"mcp_servers":"none configured"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("status is missing %s\ngot: %s", want, got)
		}
	}
}

// TestADeclaredNameIsNotTheRegisteredName pins the coupling directly: the same
// report, read by a backend whose registered name is not its check prefix, must
// still be read correctly. Deriving the key would fail this and only this.
func TestADeclaredNameIsNotTheRegisteredName(t *testing.T) {
	b := statusBackend{name: "some-agent", checks: backend.StatusChecks{Auth: "unrelated_auth_check"}}
	rep := statusReport(lima.CheckResult{Name: "unrelated_auth_check", OK: true, Detail: backend.CredentialPresent})
	if got := renderStatus(t, b, rep); !strings.Contains(got, `"credentials":"present"`) {
		t.Errorf("status did not read the declared check name\ngot: %s", got)
	}
}

// TestNoAuthCheckIsNotApplicableRatherThanAbsent keeps the honest half of the
// old behaviour. A backend that declares no auth check has not been found to be
// logged out, and Hermes is such a backend: it takes its provider credential
// from the operator's session rather than holding one.
func TestNoAuthCheckIsNotApplicableRatherThanAbsent(t *testing.T) {
	b := statusBackend{name: "hermes", checks: backend.StatusChecks{Version: "hermes_version"}}
	rep := statusReport(lima.CheckResult{Name: "hermes_version", OK: true, Detail: "0.4.0"})
	if got := renderStatus(t, b, rep); !strings.Contains(got, `"credentials":"not-applicable"`) {
		t.Errorf("a backend with no auth check must be not-applicable\ngot: %s", got)
	}
}

// TestADeclaredCheckWithNoResultIsUnknown separates the two ways an answer can
// be missing. No declared check means there was no way to ask. A declared check
// with no result means there was a way and no answer came back, and reporting
// that as not-applicable would claim the first while observing the second.
func TestADeclaredCheckWithNoResultIsUnknown(t *testing.T) {
	b := statusBackend{name: "claude-code", checks: backend.StatusChecks{Auth: "claude_auth"}}
	rep := statusReport(lima.CheckResult{Name: "claude_version", OK: true, Detail: "2.1.220"})
	if got := renderStatus(t, b, rep); !strings.Contains(got, `"credentials":"unknown"`) {
		t.Errorf("a declared check with no result must be unknown\ngot: %s", got)
	}
}

// TestALoggedOutBoxIsAbsent is the other direction: the check ran and did not
// find a credential. That is a real observation and must not soften into one of
// the two "I did not find out" answers.
func TestALoggedOutBoxIsAbsent(t *testing.T) {
	b := statusBackend{name: "claude-code", checks: backend.StatusChecks{Auth: "claude_auth"}}
	rep := statusReport(lima.CheckResult{Name: "claude_auth", OK: true, Detail: backend.CredentialAbsent})
	if got := renderStatus(t, b, rep); !strings.Contains(got, `"credentials":"absent"`) {
		t.Errorf("a probed logged-out box must be absent\ngot: %s", got)
	}
}

// TestAnUnrecognizedCredentialDetailIsUnknown pins the failure the shared
// constants exist to prevent. The state used to be recovered from the prose —
// a detail starting "credent" and containing "present" — so "credential not
// present", the most natural rewording of the absent answer, reported a
// credential the box does not hold.
//
// Equality against the constants makes that impossible, and anything else is
// "unknown": a detail this renderer cannot read is an answer it did not get,
// which is exactly what unknown means.
func TestAnUnrecognizedCredentialDetailIsUnknown(t *testing.T) {
	b := statusBackend{name: "claude-code", checks: backend.StatusChecks{Auth: "claude_auth"}}
	for _, detail := range []string{
		"credential not present",
		"credential absent; run `torio backend login`",
		"",
	} {
		rep := statusReport(lima.CheckResult{Name: "claude_auth", OK: true, Detail: detail})
		if got := renderStatus(t, b, rep); !strings.Contains(got, `"credentials":"unknown"`) {
			t.Errorf("detail %q was read as a credential state\ngot: %s", detail, got)
		}
	}
}

// TestTheAuthCheckRecordsWhatTheRendererReads closes the loop across the
// package boundary: the backend that records a credential answer and the
// renderer that reads one must be using the same two strings, and only a real
// backend can prove that.
func TestTheAuthCheckRecordsWhatTheRendererReads(t *testing.T) {
	b := claudecode.New()
	for exit, want := range map[int]string{0: `"credentials":"present"`, 1: `"credentials":"absent"`} {
		r := &recordingDetailRunner{exit: exit}
		if err := b.ProbeAuth(context.Background(), r); err != nil {
			t.Fatalf("ProbeAuth: %v", err)
		}
		rep := statusReport(lima.CheckResult{Name: b.StatusChecks().Auth, OK: true, Detail: r.detail})
		if got := renderStatus(t, b, rep); !strings.Contains(got, want) {
			t.Errorf("the renderer did not read the detail the backend recorded (%q) as %s\ngot: %s", r.detail, want, got)
		}
	}
}

// recordingDetailRunner answers the credential probe with a chosen exit code
// and keeps the detail recorded, which is the value the renderer is handed.
type recordingDetailRunner struct {
	recordingRunner
	exit   int
	detail string
}

func (r *recordingDetailRunner) Probe(ctx context.Context, name string, argv ...string) (execx.Result, error) {
	_, _ = r.recordingRunner.Probe(ctx, name, argv...)
	return execx.Result{ExitCode: r.exit}, nil
}

func (r *recordingDetailRunner) Record(name string, ok bool, detail string) {
	r.recordingRunner.Record(name, ok, detail)
	r.detail = detail
}

// TestBootstrapTellsAnUnauthenticatedBoxToLogIn pins the second reader of the
// same value. `vm bootstrap` chooses the next step from the credential state,
// so the derived key sent every Claude Code operator to `project add` on a box
// that could not yet answer.
func TestBootstrapTellsAnUnauthenticatedBoxToLogIn(t *testing.T) {
	b := statusBackend{name: "claude-code", checks: backend.StatusChecks{Auth: "claude_auth"}}
	var out bytes.Buffer
	a := &app{stdout: &out, stderr: &bytes.Buffer{}, build: testBuild(), backend: b}
	rep := statusReport(lima.CheckResult{Name: "claude_auth", OK: true, Detail: backend.CredentialAbsent})
	if err := a.writeBootstrapNextStep(rep); err != nil {
		t.Fatalf("writeBootstrapNextStep: %v", err)
	}
	if !strings.Contains(out.String(), "torio backend login") {
		t.Errorf("bootstrap did not point an unauthenticated box at login\ngot: %s", out.String())
	}
}

// TestAnUndeclaredCheckMatchesNothing pins the guard that keeps an empty
// declaration from behaving like a name. A backend that declares no MCP check
// has said it is not an MCP client, and a report entry that carries no name of
// its own must not become that backend's answer by matching it.
func TestAnUndeclaredCheckMatchesNothing(t *testing.T) {
	b := statusBackend{name: "claude-code", checks: backend.StatusChecks{Auth: "claude_auth"}}
	rep := statusReport(
		lima.CheckResult{Name: "claude_auth", OK: true, Detail: backend.CredentialPresent},
		lima.CheckResult{Name: "", OK: true, Detail: "atlassian"},
	)
	got := renderStatus(t, b, rep)
	if strings.Contains(got, "mcp_servers") {
		t.Errorf("a backend declaring no MCP check reported servers\ngot: %s", got)
	}
	if strings.Contains(got, `"version":"atlassian"`) {
		t.Errorf("an unnamed check was read as the version\ngot: %s", got)
	}
}

// TestBackendStatusChangesNothing pins the command's own promise against the
// walk it runs. `backend status` and `vm bootstrap` share that walk, and status
// asked for the repairing one: on a guest whose command path had drifted it
// would repoint a root-owned symlink — and on a fresh one download and install
// a binary — while its help text said it read the guest and changed nothing.
func TestBackendStatusChangesNothing(t *testing.T) {
	script := bootstrapHappyResp()
	script[10] = scriptedResp{res: execx.Result{ExitCode: 1}} // shim points nowhere
	fake := &fakeLimaRunner{script: script}

	code, _, stderr := runVMWithFake(t, []string{"backend", "status"}, fake)
	if code == int(ExitOK) {
		t.Fatal("backend status reported a drifted guest as fine")
	}
	if !strings.Contains(stderr, "torio vm bootstrap") {
		t.Errorf("the failure does not name the command that repairs it\ngot: %s", stderr)
	}
	for i, call := range fake.calls {
		for _, arg := range call {
			if arg == "ln" {
				t.Fatalf("backend status repaired the guest: call %d = %v", i, call)
			}
		}
	}
}

// TestTheRealBackendsDeclareTheChecksTheyRecord closes the loop the unit tests
// above cannot: a fake can declare any name and be internally consistent, so it
// proves the renderer and nothing about the two backends that ship.
//
// Every declared check is covered, because every one of them is recorded
// through a contract method the bootstrap walk calls. That was not true while
// the Hermes MCP check was recorded only by the broker verifier: it was
// declared to a renderer that reads the bootstrap report, so `backend status`
// showed nothing on a box with MCP servers configured — the same silence a
// backend that declares no check at all produces.
func TestTheRealBackendsDeclareTheChecksTheyRecord(t *testing.T) {
	for _, b := range []backend.Backend{lima.Hermes(), claudecode.New()} {
		name := b.Identity().Name
		checks := b.StatusChecks()
		r := &recordingRunner{}
		// Probes answer an empty guest, so a step may decide it has failed.
		// The runner records the name and lets the walk continue: the name is
		// what this test is about, not the verdict.
		_ = b.VerifyVersion(context.Background(), r)
		_ = b.ProbeAuth(context.Background(), r)
		_ = b.VerifyGuardrails(context.Background(), r)
		for _, declared := range []struct{ role, want string }{
			{"version", checks.Version},
			{"auth", checks.Auth},
			{"MCP servers", checks.MCPServers},
		} {
			if declared.want == "" {
				continue
			}
			if !r.saw(declared.want) {
				t.Errorf("backend %q declares %s check %q but recorded %v",
					name, declared.role, declared.want, r.names)
			}
		}
	}
}

// recordingRunner captures the names a backend's steps use. Probes answer an
// empty result and Fail returns nil, so a step that decides the guest is broken
// still records its name and the walk continues to the next one.
type recordingRunner struct{ names []string }

func (r *recordingRunner) Probe(_ context.Context, name string, _ ...string) (execx.Result, error) {
	r.names = append(r.names, name)
	return execx.Result{}, nil
}

func (r *recordingRunner) ProbeInput(_ context.Context, name string, _ []byte, _ []string) (execx.Result, error) {
	r.names = append(r.names, name)
	return execx.Result{}, nil
}

func (r *recordingRunner) Record(name string, _ bool, _ string) { r.names = append(r.names, name) }

func (r *recordingRunner) Fail(name, _, _ string) error {
	r.names = append(r.names, name)
	return nil
}

func (r *recordingRunner) PinnedVersion() string { return "" }

func (r *recordingRunner) Reconcile() bool { return true }

func (r *recordingRunner) saw(name string) bool {
	for _, n := range r.names {
		if n == name {
			return true
		}
	}
	return false
}
