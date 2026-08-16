package lima

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/execx"
)

const bootstrapTestOperator = "operator"

// bootstrapHappyScript is the ordered runner script for a fully-verified
// target: every agnostic guest postcondition passes. Order mirrors
// Adapter.Bootstrap's fixed sequence. The backend's own steps are no-ops on the
// fixture backend, so nothing here stands in for them.
func bootstrapHappyScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))}, // 0 list
		{result: stdoutResult("torio-projects:x:1001:" + testUser + "\n")},   // 1 getent group
		{result: stdoutResult("operator\n")},                                 // 2 id -un (guest session identity)
		{result: stdoutResult("operator torio-projects\n")},                  // 3 id -nG (guest session groups)
		{result: stdoutResult(testProfile.Arch + "\n")},                      // 4 uname -m (this host's guest arch)
		{result: stdoutResult("git version 2.43.0\n")},                       // 5 git --version
		{result: stdoutResult("directory\n")},                                // 6 stat testHome type
		{result: stdoutResult(testUser + ":torio-projects 710\n")},           // 7 stat testHome og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 8 findmnt testHome
		{result: stdoutResult("directory\n")},                                // 9 stat testProfilePath type
		{result: stdoutResult(testUser + ":" + testUser + " 750\n")},         // 10 stat testProfilePath og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 11 findmnt testProfilePath
		{result: stdoutResult("directory\n")},                                // 12 stat testBrainPath type
		{result: stdoutResult(testUser + ":" + testUser + " 750\n")},         // 13 stat testBrainPath og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 14 findmnt testBrainPath
		{result: stdoutResult("directory\n")},                                // 15 stat testWorkspacePath type
		{result: stdoutResult(testUser + ":torio-projects 2770\n")},          // 16 stat testWorkspacePath og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 17 findmnt testWorkspacePath
		{result: exitResult(1, "", "")},                                      // 18 findmnt host-shares
		{result: stdoutResult("regular file\n")},                             // 19 stat helper type
		{result: stdoutResult("root:root 755\n")},                            // 20 stat helper owner/mode
		{result: stdoutResult("regular file\n")},                             // 21 stat enter helper type
		{result: stdoutResult("root:root 755\n")},                            // 22 stat enter helper owner/mode
	}
}

func bootstrapOpts() BootstrapOptions {
	return BootstrapOptions{OperatorUser: bootstrapTestOperator, Backend: newTestBackend()}
}

func TestBootstrapHappyPathAllChecksPass(t *testing.T) {
	fr := &fakeRunner{script: bootstrapHappyScript()}
	a := New(fr)

	rep, err := a.Bootstrap(context.Background(), bootstrapOpts())
	if err != nil {
		t.Fatalf("Bootstrap: unexpected error: %v", err)
	}
	if rep.Instance != InstanceName {
		t.Fatalf("Instance = %q, want %q", rep.Instance, InstanceName)
	}
	for _, c := range rep.Checks {
		if !c.OK {
			t.Errorf("check %q not OK: %s", c.Name, c.Detail)
		}
	}
	if fr.callCount() != len(bootstrapHappyScript()) {
		t.Fatalf("callCount = %d, want %d (nothing mutating when every check is reconciled)",
			fr.callCount(), len(bootstrapHappyScript()))
	}

	// The agnostic probes run through the same stable path: a non-interactive
	// guest shell rooted at /, with a fixed argv and no shell interpolation.
	got := fr.callArgs(5)
	want := []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "git", "--version"}
	if !equalArgs(got, want) {
		t.Fatalf("git stable-path argv = %v, want %v", got, want)
	}
}

// TestAgentSessionHelperRejectsUnsafeBackendPathBeforeGuestAccess pins the
// backend declaration seam. HelperPath is spliced into a root shell script when
// the helper is absent, so no malformed path may reach even the first guest
// probe, regardless of whether reconciliation is enabled.
func TestAgentSessionHelperRejectsUnsafeBackendPathBeforeGuestAccess(t *testing.T) {
	for _, helperPath := range []string{
		"",
		"usr/local/bin/torio-agent-session",
		"/",
		"/usr/local/bin/../torio-agent-session",
		"/usr/local//bin/torio-agent-session",
		"/usr/local/bin/torio agent session",
		"/usr/local/bin/torio-agent-session;id",
	} {
		for _, reconcile := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reconcile=%t", helperPath, reconcile), func(t *testing.T) {
				fr := &fakeRunner{}
				a := New(fr)
				rep := BootstrapReport{}
				session := &backend.SessionSpec{
					HelperPath: helperPath,
					Helper:     []byte("#!/bin/sh\n"),
				}

				if err := a.verifyAgentSessionHelper(context.Background(), &rep, session, reconcile); err == nil {
					t.Fatal("unsafe backend helper path was accepted")
				}
				if got := fr.callCount(); got != 0 {
					t.Fatalf("guest calls = %d, want 0: validate the backend path before crossing the guest boundary", got)
				}
			})
		}
	}
}

func TestBootstrapNotRunningIsPrecondition(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Stopped"))},
	}}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindNotRunning)
	if fr.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1 (must not touch the guest when not Running)", fr.callCount())
	}
}

func TestBootstrapMissingInstanceIsNotFound(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("")},
	}}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindNotFound)
}

func TestBootstrapAmbiguousStateFailsClosed(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Broken"))},
	}}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindAmbiguousState)
}

func TestBootstrapOperatorNotInTorioProjectsFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[5] = scriptedResponse{result: stdoutResult("operator\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapRejectsEmptyOperator(t *testing.T) {
	fr := &fakeRunner{script: bootstrapHappyScript()}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{Backend: newTestBackend()})
	assertKind(t, err, KindVerificationFailed)
	if fr.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1 (list only before operator validation)", fr.callCount())
	}
}

func TestBootstrapArchMismatchFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	// An architecture no profile pins. Naming the *other* supported host would
	// make this test pass on one platform and assert nothing on the other.
	s[11] = scriptedResponse{result: stdoutResult("riscv64\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapPathNotDirectoryFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[14] = scriptedResponse{result: stdoutResult("regular file\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapPathWrongOwnershipFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[18] = scriptedResponse{result: stdoutResult("root:root 750\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapPathNotNativeFilesystemFailsClosed(t *testing.T) {
	// A path backed by a macOS host share (virtiofs) violates ADR-0002.
	s := bootstrapHappyScript()
	s[16] = scriptedResponse{result: stdoutResult("virtiofs virtiofs-share\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHostMountPresentFailsClosed(t *testing.T) {
	// A broad macOS host mount must be detected and fail closed.
	s := bootstrapHappyScript()
	s[18] = scriptedResponse{result: stdoutResult(testHome + " virtiofs\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHostMountProbeFailureFailsClosed(t *testing.T) {
	// The no-broad-host-mount claim rests on findmnt's exact PASS contract: exit 1
	// with empty stdout. Any other shape — even with empty stdout — must NOT be
	// read as "no host share", or a failed/absent findmnt would silently pass.
	cases := []struct {
		name   string
		result execx.Result
	}{
		{"exit127_empty", exitResult(127, "", "findmnt: command not found\n")},
		{"exit0_empty", exitResult(0, "", "")},
		{"exit32_empty", exitResult(32, "", "findmnt: error\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := bootstrapHappyScript()
			s[18] = scriptedResponse{result: tc.result}
			fr := &fakeRunner{script: s}
			a := New(fr)

			_, err := a.Bootstrap(context.Background(), bootstrapOpts())
			assertKind(t, err, KindVerificationFailed)
		})
	}
}

// TestBootstrapVerifiesTheOperatorShellHelper proves bootstrap refuses to call a
// target ready until the guest side of `torio project shell` is actually there.
// The V1 headline flow ends in that helper, and nothing but provisioning creates
// it: without this check a bootstrapped VM reports success and then fails at the
// remote end the first time an operator tries to push.
func TestBootstrapVerifiesTheOperatorShellHelper(t *testing.T) {
	fr := &fakeRunner{script: bootstrapHappyScript()}
	a := New(fr)

	rep, err := a.Bootstrap(context.Background(), bootstrapOpts())
	if err != nil {
		t.Fatalf("Bootstrap: unexpected error: %v", err)
	}
	if !checkOK(rep, "operator_shell_helper") {
		t.Fatalf("bootstrap did not verify the operator shell helper: %+v", rep.Checks)
	}
	if got, want := fr.callArgs(19), []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "stat", "-c", "%F", OperatorShellHelper}; !equalArgs(got, want) {
		t.Fatalf("helper type probe argv = %v, want %v", got, want)
	}
	if got, want := fr.callArgs(20), []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "stat", "-c", "%U:%G %a", OperatorShellHelper}; !equalArgs(got, want) {
		t.Fatalf("helper ownership probe argv = %v, want %v", got, want)
	}
	if got := checkDetail(rep, "operator_shell_helper"); !strings.Contains(got, "root:root") {
		t.Errorf("operator_shell_helper detail = %q, want the observed ownership", got)
	}
}

func TestBootstrapVerifiesTheProjectEnterHelper(t *testing.T) {
	fr := &fakeRunner{script: bootstrapHappyScript()}
	a := New(fr)

	rep, err := a.Bootstrap(context.Background(), bootstrapOpts())
	if err != nil {
		t.Fatalf("Bootstrap: unexpected error: %v", err)
	}
	if !checkOK(rep, "project_enter_helper") {
		t.Fatalf("bootstrap did not verify the project enter helper: %+v", rep.Checks)
	}
	if got, want := fr.callArgs(21), []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "stat", "-c", "%F", ProjectEnterHelper}; !equalArgs(got, want) {
		t.Fatalf("enter helper type probe argv = %v, want %v", got, want)
	}
	if got, want := fr.callArgs(22), []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "stat", "-c", "%U:%G %a", ProjectEnterHelper}; !equalArgs(got, want) {
		t.Fatalf("enter helper ownership probe argv = %v, want %v", got, want)
	}
}

func TestBootstrapInstallsAMissingProjectEnterHelperForExistingVMs(t *testing.T) {
	script := bootstrapHappyScript()
	script[21] = scriptedResponse{result: exitResult(1, "", "")}
	inserted := []scriptedResponse{
		{result: exitResult(0, "", "")},               // test ! -e: absent
		{result: exitResult(0, "", "")},               // the atomic root install
		{result: exitResult(0, "regular file\n", "")}, // stat -c %F, again
	}
	script = append(script[:22], append(inserted, script[22:]...)...)
	fr := &fakeRunner{script: script}
	a := New(fr)

	rep, err := a.Bootstrap(context.Background(), bootstrapOpts())
	if err != nil {
		t.Fatalf("Bootstrap: unexpected error: %v", err)
	}
	if !checkOK(rep, "project_enter_helper_installed") || !checkOK(rep, "project_enter_helper") {
		t.Fatalf("bootstrap did not install and verify the helper: %+v", rep.Checks)
	}
	want := []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "sudo", "-n", "/bin/bash", "-ceu", projectEnterInstallScript}
	if got := fr.callArgs(23); !equalArgs(got, want) {
		t.Fatalf("install argv = %v, want %v", got, want)
	}
	wantHelper, err := projectHelper(embeddedProjectEnter, testWorkspacePath, "project enter")
	if err != nil {
		t.Fatalf("resolving the helper: %v", err)
	}
	if got := fr.callStdin(23); string(got) != string(wantHelper) {
		t.Fatal("install stdin is not the helper resolved for this backend's workspace")
	}
}

// TestBootstrapOperatorShellHelperDriftFailsClosed pins every shape of helper
// drift that must stop a bootstrap. The helper runs inside a session holding the
// operator's forwarded agent, so a helper that is not a root-owned, non-writable
// regular file is a way to borrow that agent — and an unreadable one proves
// nothing and must not pass either.
func TestBootstrapOperatorShellHelperDriftFailsClosed(t *testing.T) {
	cases := []struct {
		name       string
		kind       execx.Result // stat -c %F
		ownership  execx.Result // stat -c '%U:%G %a'
		wantDetail string
	}{
		{"symlink", stdoutResult("symbolic link\n"), stdoutResult("root:root 777\n"), "want a regular file"},
		{"directory", stdoutResult("directory\n"), stdoutResult("root:root 755\n"), "want a regular file"},
		{"owned by the operator", stdoutResult("regular file\n"), stdoutResult("operator:operator 755\n"), "want root:root"},
		{"owned by the agent", stdoutResult("regular file\n"), stdoutResult(testUser + ":torio-projects 755\n"), "want root:root"},
		{"group-writable", stdoutResult("regular file\n"), stdoutResult("root:root 775\n"), "group- or world-writable"},
		{"world-writable", stdoutResult("regular file\n"), stdoutResult("root:root 757\n"), "group- or world-writable"},
		{"setgid-writable", stdoutResult("regular file\n"), stdoutResult("root:root 2775\n"), "group- or world-writable"},
		{"not executable", stdoutResult("regular file\n"), stdoutResult("root:root 644\n"), "want one of"},
		{"setuid", stdoutResult("regular file\n"), stdoutResult("root:root 4755\n"), "want one of"},
		{"unreadable mode", stdoutResult("regular file\n"), stdoutResult("root:root rwxr-xr-x\n"), "unparseable helper mode"},
		{"unreadable ownership", stdoutResult("regular file\n"), stdoutResult("\n"), "unparseable helper ownership/mode"},
		{"ownership probe failed", stdoutResult("regular file\n"), exitResult(1, "", "stat: permission denied\n"), "could not read helper ownership/mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := bootstrapHappyScript()
			s[19] = scriptedResponse{result: tc.kind}
			s[20] = scriptedResponse{result: tc.ownership}
			fr := &fakeRunner{script: s}
			a := New(fr)

			rep, err := a.Bootstrap(context.Background(), bootstrapOpts())
			assertKind(t, err, KindVerificationFailed)
			if !strings.Contains(err.Error(), tc.wantDetail) {
				t.Errorf("error = %q, want it to name %q", err.Error(), tc.wantDetail)
			}
			if !strings.Contains(err.Error(), OperatorShellHelper) && !strings.Contains(err.Error(), "helper") {
				t.Errorf("error = %q, want it to name the helper", err.Error())
			}
			if checkOK(rep, "operator_shell_helper") {
				t.Errorf("drift was recorded as a passing check: %+v", rep.Checks)
			}
		})
	}
}

func TestBootstrapRejectsTruncatedProbeOutput(t *testing.T) {
	// Bounded, truncated guest output is untrustworthy: a verify probe that was
	// cut off must fail closed rather than be parsed as ground truth.
	s := bootstrapHappyScript()
	s[11] = scriptedResponse{result: execx.Result{ExitCode: 0, Stdout: []byte(testProfile.Arch + "\n"), StdoutTruncated: true}}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapTransportTimeoutIsExternal(t *testing.T) {
	fr := &fakeRunner{
		script: []scriptedResponse{
			{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))},
		},
		respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
			return execx.Result{ExitCode: -1}, wrapErr(context.DeadlineExceeded)
		},
	}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindTimeout)
}

func TestBootstrapPropagatesContext(t *testing.T) {
	var seen context.Context
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		seen = ctx
		return stdoutResult(""), nil
	}}
	a := New(fr)

	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	_, _ = a.Bootstrap(ctx, bootstrapOpts())
	if seen == nil {
		t.Fatalf("runner never invoked")
	}
	if v, _ := seen.Value(ctxKey{}).(string); v != "marker" {
		t.Fatalf("adapter did not propagate the caller's context")
	}
}

func TestParseStatOwnership(t *testing.T) {
	owner, group, mode, ok := parseStatOwnership(testUser + ":torio-projects 710\n")
	if !ok || owner != testUser || group != "torio-projects" || mode != "710" {
		t.Fatalf("parseStatOwnership = (%q,%q,%q,%v), want %s,torio-projects,710,true", owner, group, mode, ok, testUser)
	}
}

func TestModeMatches(t *testing.T) {
	spec := newTestBackend().RequiredPaths()[0] // testHome: 710 or 0710
	if !modeMatches(spec, "710") || !modeMatches(spec, "0710") {
		t.Error("expected 710 and 0710 to match testHome spec")
	}
	if modeMatches(spec, "755") {
		t.Error("755 must not match testHome spec")
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// --- test helpers ---

func assertKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != want {
		t.Fatalf("Kind = %v, want %v (err: %v)", lerr.Kind, want, err)
	}
}

// TestVerifyOnlyRepairsNothing pins the mode that makes a status command able
// to say it changes nothing.
//
// `torio backend status` runs this same walk, and before VerifyOnly existed it
// ran the repairing one: asking a drifted guest what it was would download a
// pinned binary, repoint a root-owned symlink and write managed settings, while
// the command's own help text said it read the guest and changed nothing.
func TestVerifyOnlyRepairsNothing(t *testing.T) {
	s := bootstrapHappyScript()
	// The operator shell helper is absent, which a repairing run installs.
	s[19] = scriptedResponse{result: exitResult(1, "", "stat: cannot statx: No such file or directory\n")}
	fr := &fakeRunner{script: s}

	_, err := New(fr).Bootstrap(context.Background(), BootstrapOptions{
		OperatorUser: bootstrapTestOperator,
		Backend:      newTestBackend(),
		VerifyOnly:   true,
	})
	if err == nil {
		t.Fatal("a verify-only run reported a missing helper as fine")
	}
	if !strings.Contains(err.Error(), "restart the VM") {
		t.Errorf("failure does not name the way to repair it: %v", err)
	}
	for i := 0; i < fr.callCount(); i++ {
		for _, arg := range fr.callArgs(i) {
			if arg == "/bin/bash" {
				t.Fatalf("a verify-only run installed the helper: call %d = %v", i, fr.callArgs(i))
			}
		}
	}
}

func checkOK(rep BootstrapReport, name string) bool {
	for _, c := range rep.Checks {
		if c.Name == name {
			return c.OK
		}
	}
	return false
}

func checkDetail(rep BootstrapReport, name string) string {
	for _, c := range rep.Checks {
		if c.Name == name {
			return c.Detail
		}
	}
	return ""
}

// TestModeMatchesAcceptsAStricterModeOnlyWhereNobodyElseNeedsThePermission
// pins the asymmetry that made a first-use machine unbootstrappable.
//
// An agent tightens its own profile to 0700 the moment it stores provider
// credentials there. Bootstrap compared modes for equality, so that ordinary
// first use turned every later verified command into a fail-closed precondition
// error. Loosening must still fail, and so must tightening a path whose group
// or world permission the operator depends on.
func TestModeMatchesAcceptsAStricterModeOnlyWhereNobodyElseNeedsThePermission(t *testing.T) {
	private := backend.PathSpec{Modes: []string{"750", "0750"}, AllowStricter: true}
	shared := backend.PathSpec{Modes: []string{"710", "0710"}}
	helper := operatorShellHelperSpec

	cases := []struct {
		name string
		spec backend.PathSpec
		mode string
		want bool
	}{
		{"private exact", private, "750", true},
		{"private zero-padded", private, "0750", true},
		{"private tightened by the agent", private, "700", true},
		{"private tightened, zero-padded", private, "0700", true},
		{"private loosened to group-writable", private, "770", false},
		{"private loosened to world-readable", private, "755", false},
		{"private owner locked out", private, "050", false},
		{"private setuid smuggled in", private, "4750", false},
		{"shared path keeps its traversal bit", shared, "700", false},
		{"shared path exact", shared, "710", true},
		{"helper stays executable by all", helper, "700", false},
		{"helper exact", helper, "755", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modeMatches(tc.spec, tc.mode); got != tc.want {
				t.Errorf("modeMatches(%v, %q) = %v, want %v", tc.spec.Modes, tc.mode, got, tc.want)
			}
		})
	}
}

// TestBootstrapRequiredPathsOptInToStrictnessDeliberately keeps the exemption
// from spreading: only the two directories private to the agent may be
// tightened, because their group is the agent itself. testHome and
// testWorkspacePath carry torio-projects permissions the operator's own session
// uses.
func TestBootstrapRequiredPathsOptInToStrictnessDeliberately(t *testing.T) {
	want := map[string]bool{
		testHome:          false,
		testProfilePath:   true,
		testBrainPath:     true,
		testWorkspacePath: false,
	}
	for _, spec := range newTestBackend().RequiredPaths() {
		expected, known := want[spec.Path]
		if !known {
			t.Errorf("unexpected required path %q: decide whether a stricter mode is drift or hardening", spec.Path)
			continue
		}
		if spec.AllowStricter != expected {
			t.Errorf("%s allowStricter = %v, want %v", spec.Path, spec.AllowStricter, expected)
		}
	}
}

// TestBootstrapInstallsAMissingOperatorShellHelperForExistingVMs is the delivery
// path for a corrected helper.
//
// The Lima template is rendered once, at `vm init`, so before this the only way
// to change the push-capable helper on a box that already existed was to
// recreate the box. The three responses inserted below are the calls that branch
// adds — prove absent, install, re-probe — and the ownership response already in
// the script is what verifies the result.
func TestBootstrapInstallsAMissingOperatorShellHelperForExistingVMs(t *testing.T) {
	script := bootstrapHappyScript()
	script[19] = scriptedResponse{result: exitResult(1, "", "stat: cannot statx: No such file or directory\n")}
	inserted := []scriptedResponse{
		{result: exitResult(0, "", "")},               // test ! -e: absent
		{result: exitResult(0, "", "")},               // the atomic root install
		{result: exitResult(0, "regular file\n", "")}, // stat -c %F, again
	}
	script = append(script[:20], append(inserted, script[20:]...)...)
	fr := &fakeRunner{script: script}
	a := New(fr)

	rep, err := a.Bootstrap(context.Background(), bootstrapOpts())
	if err != nil {
		t.Fatalf("Bootstrap: unexpected error: %v", err)
	}
	if !checkOK(rep, "operator_shell_helper_installed") || !checkOK(rep, "operator_shell_helper") {
		t.Fatalf("bootstrap did not install and verify the helper: %+v", rep.Checks)
	}
	wantArgs := []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "sudo", "-n", "/bin/bash", "-ceu", operatorShellInstallScript}
	if got := fr.callArgs(21); !equalArgs(got, wantArgs) {
		t.Fatalf("install argv = %v, want %v", got, wantArgs)
	}
	// The bytes that reach the guest are the shipped script resolved for this
	// backend's workspace. Installing the raw embed would put an unsubstituted
	// placeholder on the guest, which refuses every project rather than the
	// wrong ones.
	wantHelper, err := projectHelper(embeddedProjectShell, testWorkspacePath, "operator shell")
	if err != nil {
		t.Fatalf("resolving the helper: %v", err)
	}
	if got := fr.callStdin(21); string(got) != string(wantHelper) {
		t.Fatal("install stdin is not the helper resolved for this backend's workspace")
	}
}

// TestBootstrapVerifyOnlyRefusesAMissingOperatorShellHelper keeps the half of
// the old behaviour that still holds. Installing an absent helper is reconcile
// work; a run that may repair nothing must report the absence instead, because
// `torio backend status` walks this same path and must not quietly provision a
// guest while answering a question about it.
func TestBootstrapVerifyOnlyRefusesAMissingOperatorShellHelper(t *testing.T) {
	script := bootstrapHappyScript()
	script[19] = scriptedResponse{result: exitResult(1, "", "stat: cannot statx: No such file or directory\n")}
	script = append(script[:20], append([]scriptedResponse{{result: exitResult(0, "", "")}}, script[20:]...)...)
	fr := &fakeRunner{script: script}
	a := New(fr)

	opts := bootstrapOpts()
	opts.VerifyOnly = true
	rep, err := a.Bootstrap(context.Background(), opts)
	assertKind(t, err, KindVerificationFailed)
	if !strings.Contains(err.Error(), "no operator shell helper") {
		t.Errorf("error = %q, want it to name the missing helper", err.Error())
	}
	if checkOK(rep, "operator_shell_helper") {
		t.Errorf("an absent helper was recorded as a passing check: %+v", rep.Checks)
	}
}
