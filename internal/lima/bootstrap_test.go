package lima

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/execx"
)

const bootstrapTestOperator = "operator"

// bootstrapHappyScript is the ordered runner script for a fully-reconciled,
// fully-verified V1 target: Hermes install pin verified, shim correct, and all
// guest postconditions pass. Order mirrors Adapter.Bootstrap's fixed sequence.
func bootstrapHappyScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))}, // 0 list
		{result: stdoutResult("1000\n")},                                     // 1 id -u hermes
		{result: stdoutResult("torio-projects:x:1001:hermes\n")},             // 2 getent group
		{result: stdoutResult("hermes torio-projects\n")},                    // 3 id -nG hermes (torio-projects)
		{result: stdoutResult("operator\n")},                                 // 4 id -un (guest session identity)
		{result: stdoutResult("operator torio-projects\n")},                  // 5 id -nG (guest session groups)
		{result: stdoutResult("hermes torio-projects\n")},                    // 6 id -nG hermes (not docker)
		{result: exitResult(0, "", "")},                                      // 7 test -x launcher (install present)
		{result: stdoutResult(PromotedHermesCommit + "\n")},                  // 8 git rev-parse HEAD
		{result: exitResult(0, "", "")},                                      // 9 test -x launcher (shim reconcile)
		{result: stdoutResult(hermesTarget + "\n")},                          // 10 readlink shim
		{result: stdoutResult(testProfile.Arch + "\n")},                      // 11 uname -m (this host's guest arch)
		{result: stdoutResult("Hermes Agent v0.19.0 (2026.7.20)\n")},         // 12 hermes --version
		{result: stdoutResult("git version 2.43.0\n")},                       // 13 git --version
		{result: stdoutResult("directory\n")},                                // 14 stat HermesHome type
		{result: stdoutResult("hermes:torio-projects 710\n")},                // 15 stat HermesHome og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 16 findmnt HermesHome
		{result: stdoutResult("directory\n")},                                // 17 stat HermesProfilePath type
		{result: stdoutResult("hermes:hermes 750\n")},                        // 18 stat HermesProfilePath og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 19 findmnt HermesProfilePath
		{result: stdoutResult("directory\n")},                                // 20 stat HermesBrainPath type
		{result: stdoutResult("hermes:hermes 750\n")},                        // 21 stat HermesBrainPath og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 22 findmnt HermesBrainPath
		{result: stdoutResult("directory\n")},                                // 23 stat HermesWorkspacePath type
		{result: stdoutResult("hermes:torio-projects 2770\n")},               // 24 stat HermesWorkspacePath og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 25 findmnt HermesWorkspacePath
		{result: exitResult(1, "", "")},                                      // 26 findmnt host-shares
		{result: stdoutResult("regular file\n")},                             // 27 stat helper type
		{result: stdoutResult("root:root 755\n")},                            // 28 stat helper owner/mode
		{result: stdoutResult("regular file\n")},                             // 29 stat enter helper type
		{result: stdoutResult("root:root 755\n")},                            // 30 stat enter helper owner/mode
		{result: stdoutResult("regular file\n")},                             // 31 stat hermes config type
		{result: stdoutResult("model:\n  provider: custom\n")},               // 32 cat hermes config (no mcp_servers)
	}
}

func bootstrapOpts() BootstrapOptions {
	return BootstrapOptions{OperatorUser: bootstrapTestOperator}
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
	if fr.callCount() != 33 {
		t.Fatalf("callCount = %d, want 33 (no install/shim mutating steps when reconciled)", fr.callCount())
	}

	got := fr.callArgs(12)
	want := []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "sudo", "-n", "-u", HermesUser, "--", "hermes", "--version"}
	if !equalArgs(got, want) {
		t.Fatalf("hermes stable-path argv = %v, want %v", got, want)
	}
}

func TestHermesProfilePathNotEqualBrainPath(t *testing.T) {
	if HermesProfilePath == HermesBrainPath {
		t.Fatalf("profile and brain paths must be distinct: both %q", HermesProfilePath)
	}
	if HermesProfilePath != "/home/hermes/.hermes" {
		t.Errorf("HermesProfilePath = %q, want /home/hermes/.hermes", HermesProfilePath)
	}
	if HermesBrainPath != "/home/hermes/brain" {
		t.Errorf("HermesBrainPath = %q, want /home/hermes/brain", HermesBrainPath)
	}
}

func TestBootstrapReportSurfacesObservedVersions(t *testing.T) {
	fr := &fakeRunner{script: bootstrapHappyScript()}
	a := New(fr)

	rep, err := a.Bootstrap(context.Background(), bootstrapOpts())
	if err != nil {
		t.Fatalf("Bootstrap: unexpected error: %v", err)
	}
	if got := checkDetail(rep, "hermes_version"); got == "" {
		t.Errorf("hermes_version detail must report the observed version, got empty")
	}
	if got := checkDetail(rep, "git"); got != "2.43.0" {
		t.Errorf("git detail = %q, want 2.43.0", got)
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

func TestBootstrapReconcilesHermesShim(t *testing.T) {
	s := bootstrapHappyScript()
	s[10] = scriptedResponse{result: exitResult(1, "", "")} // readlink shim missing
	s = append(s[:11], append([]scriptedResponse{
		{result: exitResult(0, "", "")}, // ln -sfn
	}, s[11:]...)...)
	fr := &fakeRunner{script: s}
	a := New(fr)

	rep, err := a.Bootstrap(context.Background(), bootstrapOpts())
	if err != nil {
		t.Fatalf("Bootstrap: unexpected error: %v", err)
	}
	if fr.callCount() != 34 {
		t.Fatalf("callCount = %d, want 34 (ln reconcile step present)", fr.callCount())
	}
	if got, want := fr.callArgs(11), []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "sudo", "-n", "ln", "-sfn", hermesTarget, hermesShimPath}; !equalArgs(got, want) {
		t.Fatalf("ln argv = %v, want %v", got, want)
	}
	if !checkOK(rep, "hermes_shim") {
		t.Fatalf("reconcile check should be OK after repair: %+v", rep.Checks)
	}
}

func TestBootstrapMissingLauncherInstallsHermes(t *testing.T) {
	s := bootstrapHappyScript()
	s[7] = scriptedResponse{result: exitResult(1, "", "")} // launcher missing
	install := []scriptedResponse{
		{result: exitResult(0, "", "")},                     // apt-get update
		{result: exitResult(0, "", "")},                     // apt-get install deps
		{result: exitResult(0, "", "")},                     // curl install.sh
		{result: exitResult(0, "", "")},                     // bash install.sh
		{result: exitResult(0, "", "")},                     // rm install script
		{result: exitResult(0, "", "")},                     // test -x launcher after install
		{result: stdoutResult(PromotedHermesCommit + "\n")}, // git rev-parse HEAD
	}
	s = append(s[:8], append(install, s[8:]...)...)
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	if err != nil {
		t.Fatalf("Bootstrap: unexpected error: %v", err)
	}
	curlIdx := -1
	for i := 0; i < fr.callCount(); i++ {
		args := fr.callArgs(i)
		for _, arg := range args {
			if arg == "curl" {
				curlIdx = i
			}
		}
	}
	if curlIdx < 0 {
		t.Fatal("expected curl argv when launcher was missing")
	}
	got := fr.callArgs(curlIdx)
	if !containsArg(got, hermesInstallScriptURL) {
		t.Fatalf("curl argv = %v, want install URL", got)
	}
}

func TestBootstrapExistingInstallSkipsCurl(t *testing.T) {
	fr := &fakeRunner{script: bootstrapHappyScript()}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	for i := 0; i < fr.callCount(); i++ {
		for _, arg := range fr.callArgs(i) {
			if arg == "curl" {
				t.Fatalf("call %d unexpectedly invoked curl: %v", i, fr.callArgs(i))
			}
		}
	}
}

func TestBootstrapInstallWrongCommitFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[8] = scriptedResponse{result: stdoutResult("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindVerificationFailed)
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

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
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

func TestBootstrapHermesVersionNonZeroExitFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[12] = scriptedResponse{result: exitResult(127, "", "hermes: command not found")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHermesVersionZeroExitButEmptyFailsClosed(t *testing.T) {
	// A clean exit is not proof: empty/unrecognized version output is unverifiable.
	s := bootstrapHappyScript()
	s[12] = scriptedResponse{result: exitResult(0, "", "")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), bootstrapOpts())
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHermesPinnedVersionMismatchIsDrift(t *testing.T) {
	s := bootstrapHappyScript()
	s[12] = scriptedResponse{result: stdoutResult("Hermes Agent v0.19.0 (2026.7.20)\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{PinnedVersion: "0.20.0"})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHermesInDockerGroupFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[6] = scriptedResponse{result: stdoutResult("hermes docker torio-projects\n")}
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
	s[26] = scriptedResponse{result: stdoutResult("/home/hermes virtiofs\n")}
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
			s[26] = scriptedResponse{result: tc.result}
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
	if got, want := fr.callArgs(27), []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "stat", "-c", "%F", OperatorShellHelper}; !equalArgs(got, want) {
		t.Fatalf("helper type probe argv = %v, want %v", got, want)
	}
	if got, want := fr.callArgs(28), []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "stat", "-c", "%U:%G %a", OperatorShellHelper}; !equalArgs(got, want) {
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
	if got, want := fr.callArgs(29), []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "stat", "-c", "%F", ProjectEnterHelper}; !equalArgs(got, want) {
		t.Fatalf("enter helper type probe argv = %v, want %v", got, want)
	}
	if got, want := fr.callArgs(30), []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "stat", "-c", "%U:%G %a", ProjectEnterHelper}; !equalArgs(got, want) {
		t.Fatalf("enter helper ownership probe argv = %v, want %v", got, want)
	}
}

func TestBootstrapInstallsAMissingProjectEnterHelperForExistingVMs(t *testing.T) {
	script := bootstrapHappyScript()
	script[29] = scriptedResponse{result: exitResult(1, "", "")}
	script = append(script[:30],
		scriptedResponse{result: exitResult(0, "", "")},
		scriptedResponse{result: exitResult(0, "", "")},
		scriptedResponse{result: exitResult(0, "regular file\n", "")},
		scriptedResponse{result: exitResult(0, "root:root 755\n", "")},
		scriptedResponse{result: exitResult(0, "regular file\n", "")},
		scriptedResponse{result: exitResult(0, "model:\n  provider: custom\n", "")},
	)
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
	if got := fr.callArgs(31); !equalArgs(got, want) {
		t.Fatalf("install argv = %v, want %v", got, want)
	}
	if got := fr.callStdin(31); string(got) != string(embeddedProjectEnter) {
		t.Fatal("install stdin does not match the embedded project enter helper")
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
		{"missing", exitResult(1, "", "stat: cannot statx: No such file or directory\n"), stdoutResult("root:root 755\n"), "no operator shell helper"},
		{"symlink", stdoutResult("symbolic link\n"), stdoutResult("root:root 777\n"), "want a regular file"},
		{"directory", stdoutResult("directory\n"), stdoutResult("root:root 755\n"), "want a regular file"},
		{"owned by the operator", stdoutResult("regular file\n"), stdoutResult("operator:operator 755\n"), "want root:root"},
		{"owned by hermes", stdoutResult("regular file\n"), stdoutResult("hermes:torio-projects 755\n"), "want root:root"},
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
			s[27] = scriptedResponse{result: tc.kind}
			s[28] = scriptedResponse{result: tc.ownership}
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
	owner, group, mode, ok := parseStatOwnership("hermes:torio-projects 710\n")
	if !ok || owner != "hermes" || group != "torio-projects" || mode != "710" {
		t.Fatalf("parseStatOwnership = (%q,%q,%q,%v), want hermes,torio-projects,710,true", owner, group, mode, ok)
	}
}

func TestModeMatches(t *testing.T) {
	spec := bootstrapRequiredPaths[0] // HermesHome: 710 or 0710
	if !modeMatches(spec, "710") || !modeMatches(spec, "0710") {
		t.Error("expected 710 and 0710 to match HermesHome spec")
	}
	if modeMatches(spec, "755") {
		t.Error("755 must not match HermesHome spec")
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

// TestBootstrapRecordsTheMCPServersItDeclares closes the gap between what this
// backend declares in StatusChecks and what the report an operator reads
// actually carries. The check existed and ran, but only inside VerifyMCPBroker,
// whose report `torio backend status` never sees — so a Hermes box wired to MCP
// showed nothing, exactly as a backend that is not an MCP client would.
//
// Both directions are pinned, because a check that can only say "fine" would be
// no better than the silence it replaced.
func TestBootstrapRecordsTheMCPServersItDeclares(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		wantOK bool
		want   string
	}{
		{"through the relay", "mcp_servers:\n  atlassian:\n    command: " + TorioMCPRelayPath + "\n", true, "1 entr(ies), all through the relay"},
		{"bypassing it", "mcp_servers:\n  sneaky:\n    command: /usr/bin/npx\n", false, "1 of 1 MCP server entries do not go through the broker relay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := bootstrapHappyScript()
			script[32] = scriptedResponse{result: stdoutResult(tc.config)}
			rep, err := New(&fakeRunner{script: script}).Bootstrap(context.Background(), bootstrapOpts())
			// A configured MCP server is a fact about the box, not a defect in
			// it: the command that fails closed on a bypass is `torio mcp
			// status`, where the boundary itself is what is being verified.
			if err != nil {
				t.Fatalf("recording the MCP servers failed the bootstrap: %v", err)
			}
			if got := Hermes().StatusChecks().MCPServers; checkDetail(rep, got) != tc.want {
				t.Errorf("%s detail = %q, want %q", got, checkDetail(rep, got), tc.want)
			}
			if got := checkOK(rep, Hermes().StatusChecks().MCPServers); got != tc.wantOK {
				t.Errorf("check OK = %v, want %v", got, tc.wantOK)
			}
		})
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
	s[10] = scriptedResponse{result: exitResult(1, "", "")} // shim points nowhere
	fr := &fakeRunner{script: s}

	_, err := New(fr).Bootstrap(context.Background(), BootstrapOptions{
		OperatorUser: bootstrapTestOperator,
		VerifyOnly:   true,
	})
	if err == nil {
		t.Fatal("a verify-only run reported a drifted shim as fine")
	}
	if !strings.Contains(err.Error(), "torio vm bootstrap") {
		t.Errorf("failure does not name the command that repairs it: %v", err)
	}
	for i := 0; i < fr.callCount(); i++ {
		for _, arg := range fr.callArgs(i) {
			if arg == "ln" {
				t.Fatalf("a verify-only run repaired the shim: call %d = %v", i, fr.callArgs(i))
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
// Hermes tightens /home/hermes/.hermes to 0700 the moment it stores provider
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
		{"private tightened by hermes", private, "700", true},
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
// from spreading: only the two directories private to hermes may be tightened,
// because their group is hermes itself. HermesHome and HermesWorkspacePath
// carry torio-projects permissions the operator's own session uses.
func TestBootstrapRequiredPathsOptInToStrictnessDeliberately(t *testing.T) {
	want := map[string]bool{
		HermesHome:          false,
		HermesProfilePath:   true,
		HermesBrainPath:     true,
		HermesWorkspacePath: false,
	}
	for _, spec := range bootstrapRequiredPaths {
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
