package lima

import (
	"context"
	"errors"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

// bootstrapHappyScript is the ordered runner script for a fully-reconciled,
// fully-verified V1 target: the shim already points at the pinned hermes launcher
// and all guest postconditions pass, so no mutating step fires. The order mirrors
// Adapter.Bootstrap's fixed call sequence and is the baseline the failure tests
// below diverge from.
func bootstrapHappyScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))}, // 0 list (Running precondition)
		{result: exitResult(0, "", "")},                                      // 1 test -x <target> (install present)
		{result: stdoutResult(hermesTarget + "\n")},                          // 2 readlink shim (already correct)
		{result: stdoutResult("1000\n")},                                     // 3 id -u hermes
		{result: stdoutResult("torio-projects:x:1001:hermes\n")},             // 4 getent group torio-projects
		{result: stdoutResult("hermes torio-projects\n")},                    // 5 id -nG hermes (torio-projects member)
		{result: stdoutResult("hermes torio-projects\n")},                    // 6 id -nG hermes (not in docker)
		{result: stdoutResult("aarch64\n")},                                  // 7 uname -m
		{result: stdoutResult("Hermes Agent v0.19.0 (2026.7.20)\n")},         // 8 hermes --version (stable path)
		{result: stdoutResult("git version 2.43.0\n")},                       // 9 git --version
		{result: stdoutResult("directory\n")},                                // 10 stat HermesHome type
		{result: stdoutResult("hermes:torio-projects 710\n")},                // 11 stat HermesHome owner/group/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 12 findmnt HermesHome
		{result: stdoutResult("directory\n")},                                // 13 stat HermesProfilePath type
		{result: stdoutResult("hermes:hermes 750\n")},                        // 14 stat HermesProfilePath owner/group/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 15 findmnt HermesProfilePath
		{result: stdoutResult("directory\n")},                                // 16 stat HermesBrainPath type
		{result: stdoutResult("hermes:hermes 750\n")},                        // 17 stat HermesBrainPath owner/group/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 18 findmnt HermesBrainPath
		{result: stdoutResult("directory\n")},                                // 19 stat HermesWorkspacePath type
		{result: stdoutResult("hermes:torio-projects 2770\n")},               // 20 stat HermesWorkspacePath owner/group/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 21 findmnt HermesWorkspacePath
		{result: exitResult(1, "", "")},                                        // 22 findmnt host-shares (none → exit 1, empty)
	}
}

func TestBootstrapHappyPathAllChecksPass(t *testing.T) {
	fr := &fakeRunner{script: bootstrapHappyScript()}
	a := New(fr)

	rep, err := a.Bootstrap(context.Background(), BootstrapOptions{})
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
	if fr.callCount() != 23 {
		t.Fatalf("callCount = %d, want 23 (no mutating step when already reconciled)", fr.callCount())
	}

	// The hermes verification must run through the documented stable command
	// path: as the hermes service user, via the bare `hermes` name (resolved by
	// the shim on the sudo secure_path) — never a raw absolute venv path.
	got := fr.callArgs(8)
	want := []string{"shell", "--tty=false", InstanceName, "--", "sudo", "-n", "-u", HermesUser, "--", "hermes", "--version"}
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

	rep, err := a.Bootstrap(context.Background(), BootstrapOptions{})
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

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
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

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindNotFound)
}

func TestBootstrapAmbiguousStateFailsClosed(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Broken"))},
	}}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindAmbiguousState)
}

func TestBootstrapReconcilesHermesShim(t *testing.T) {
	// The shim is missing: the narrow reconcile step must fire (ln), then full
	// verification proceeds.
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))}, // 0 list
		{result: exitResult(0, "", "")},                                      // 1 test -x target
		{result: exitResult(1, "", "")},                                      // 2 readlink shim (missing → exit 1)
		{result: exitResult(0, "", "")},                                      // 3 ln -sfn target shim
		{result: stdoutResult("1000\n")},                                     // 4 id -u hermes
		{result: stdoutResult("torio-projects:x:1001:hermes\n")},             // 5 getent group torio-projects
		{result: stdoutResult("hermes torio-projects\n")},                    // 6 id -nG hermes (torio-projects)
		{result: stdoutResult("hermes torio-projects\n")},                    // 7 id -nG hermes (not docker)
		{result: stdoutResult("aarch64\n")},                                  // 8 uname -m
		{result: stdoutResult("Hermes Agent v0.19.0\n")},                     // 9 hermes --version
		{result: stdoutResult("git version 2.43.0\n")},                       // 10 git --version
		{result: stdoutResult("directory\n")},                                // 11 stat home type
		{result: stdoutResult("hermes:torio-projects 710\n")},                // 12 stat home og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 13 findmnt home
		{result: stdoutResult("directory\n")},                                // 14 stat profile type
		{result: stdoutResult("hermes:hermes 750\n")},                        // 15 stat profile og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 16 findmnt profile
		{result: stdoutResult("directory\n")},                                // 17 stat brain type
		{result: stdoutResult("hermes:hermes 750\n")},                        // 18 stat brain og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 19 findmnt brain
		{result: stdoutResult("directory\n")},                                // 20 stat workspace type
		{result: stdoutResult("hermes:torio-projects 2770\n")},               // 21 stat workspace og/mode
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 22 findmnt workspace
		{result: exitResult(1, "", "")},                                        // 23 findmnt host-shares
	}}
	a := New(fr)

	rep, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	if err != nil {
		t.Fatalf("Bootstrap: unexpected error: %v", err)
	}
	if fr.callCount() != 24 {
		t.Fatalf("callCount = %d, want 24 (ln reconcile step present)", fr.callCount())
	}
	if got, want := fr.callArgs(3), []string{"shell", "--tty=false", InstanceName, "--", "sudo", "-n", "ln", "-sfn", hermesTarget, hermesShimPath}; !equalArgs(got, want) {
		t.Fatalf("ln argv = %v, want %v", got, want)
	}
	if !checkOK(rep, "hermes_shim") {
		t.Fatalf("reconcile check should be OK after repair: %+v", rep.Checks)
	}
}

func TestBootstrapMissingHermesInstallIsDrift(t *testing.T) {
	// The pinned launcher is absent (test -x fails): bootstrap must report drift
	// and must NOT create a dangling shim.
	s := bootstrapHappyScript()
	s[1] = scriptedResponse{result: exitResult(1, "", "")} // test -x target → missing
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapArchMismatchFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[7] = scriptedResponse{result: stdoutResult("x86_64\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHermesVersionNonZeroExitFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[8] = scriptedResponse{result: exitResult(127, "", "hermes: command not found")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHermesVersionZeroExitButEmptyFailsClosed(t *testing.T) {
	// A clean exit is not proof: empty/unrecognized version output is unverifiable.
	s := bootstrapHappyScript()
	s[8] = scriptedResponse{result: exitResult(0, "", "")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHermesPinnedVersionMismatchIsDrift(t *testing.T) {
	s := bootstrapHappyScript()
	s[8] = scriptedResponse{result: stdoutResult("Hermes Agent v0.19.0 (2026.7.20)\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{HermesVersion: "0.20.0"})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHermesInDockerGroupFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[6] = scriptedResponse{result: stdoutResult("hermes docker torio-projects\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapPathNotDirectoryFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[10] = scriptedResponse{result: stdoutResult("regular file\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapPathWrongOwnershipFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[14] = scriptedResponse{result: stdoutResult("root:root 750\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapPathNotNativeFilesystemFailsClosed(t *testing.T) {
	// A path backed by a macOS host share (virtiofs) violates ADR-0003.
	s := bootstrapHappyScript()
	s[12] = scriptedResponse{result: stdoutResult("virtiofs virtiofs-share\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHostMountPresentFailsClosed(t *testing.T) {
	// A broad macOS host mount must be detected and fail closed.
	s := bootstrapHappyScript()
	s[22] = scriptedResponse{result: stdoutResult("/home/hermes virtiofs\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
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
			s[22] = scriptedResponse{result: tc.result}
			fr := &fakeRunner{script: s}
			a := New(fr)

			_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
			assertKind(t, err, KindVerificationFailed)
		})
	}
}

func TestBootstrapRejectsTruncatedProbeOutput(t *testing.T) {
	// Bounded, truncated guest output is untrustworthy: a verify probe that was
	// cut off must fail closed rather than be parsed as ground truth.
	s := bootstrapHappyScript()
	s[7] = scriptedResponse{result: execx.Result{ExitCode: 0, Stdout: []byte("aarch64\n"), StdoutTruncated: true}}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
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

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
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
	_, _ = a.Bootstrap(ctx, BootstrapOptions{})
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
