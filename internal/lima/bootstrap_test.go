package lima

import (
	"context"
	"errors"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

// bootstrapHappyScript is the ordered runner script for a fully-reconciled,
// fully-verified target: the shim already points at the pinned hermes launcher
// and hermes is already in the docker group, so no mutating step fires. The
// order mirrors Adapter.Bootstrap's fixed call sequence and is the baseline the
// failure tests below diverge from.
func bootstrapHappyScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))}, // 0 list (Running precondition)
		{result: stdoutResult("hermes docker\n")},                            // 1 id -nG hermes (in docker group)
		{result: exitResult(0, "", "")},                                      // 2 test -x <target> (install present)
		{result: stdoutResult(hermesTarget + "\n")},                          // 3 readlink shim (already correct)
		{result: stdoutResult("aarch64\n")},                                  // 4 uname -m
		{result: stdoutResult("Hermes Agent v0.19.0 (2026.7.20)\n")},         // 5 hermes --version (stable path)
		{result: stdoutResult("29.6.2\n")},                                   // 6 docker server version (as hermes)
		{result: stdoutResult("git version 2.43.0\n")},                       // 7 git --version
		{result: stdoutResult("directory\n")},                                // 8 stat path[0]
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 9 findmnt path[0]
		{result: stdoutResult("directory\n")},                                // 10 stat path[1]
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 11 findmnt path[1]
		{result: exitResult(1, "", "")},                                      // 12 findmnt host-shares (none → exit 1, empty)
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
	if fr.callCount() != 13 {
		t.Fatalf("callCount = %d, want 13 (no mutating step when already reconciled)", fr.callCount())
	}

	// The hermes verification must run through the documented stable command
	// path: as the hermes service user, via the bare `hermes` name (resolved by
	// the shim on the sudo secure_path) — never a raw absolute venv path.
	got := fr.callArgs(5)
	want := []string{"shell", "--tty=false", InstanceName, "--", "sudo", "-n", "-u", HermesUser, "--", "hermes", "--version"}
	if !equalArgs(got, want) {
		t.Fatalf("hermes stable-path argv = %v, want %v", got, want)
	}
}

func TestBootstrapReportSurfacesObservedVersions(t *testing.T) {
	fr := &fakeRunner{script: bootstrapHappyScript()}
	a := New(fr)

	rep, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	if err != nil {
		t.Fatalf("Bootstrap: unexpected error: %v", err)
	}
	// Observed versions must be reported so drift is visible even when unpinned.
	if got := checkDetail(rep, "docker_server"); got != "29.6.2" {
		t.Errorf("docker_server detail = %q, want 29.6.2", got)
	}
	if got := checkDetail(rep, "hermes_version"); got == "" {
		t.Errorf("hermes_version detail must report the observed version, got empty")
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

func TestBootstrapReconcilesDockerGroupAndShim(t *testing.T) {
	// hermes is NOT yet in the docker group and the shim is missing: both narrow
	// reconcile steps must fire (usermod, ln), then full verification proceeds.
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))}, // 0 list
		{result: stdoutResult("hermes\n")},                                   // 1 id -nG hermes (NOT in docker group)
		{result: exitResult(0, "", "")},                                      // 2 usermod -aG docker hermes
		{result: exitResult(0, "", "")},                                      // 3 test -x target
		{result: exitResult(1, "", "")},                                      // 4 readlink shim (missing → exit 1)
		{result: exitResult(0, "", "")},                                      // 5 ln -sfn target shim
		{result: stdoutResult("aarch64\n")},                                  // 6 uname -m
		{result: stdoutResult("Hermes Agent v0.19.0\n")},                     // 7 hermes --version
		{result: stdoutResult("29.6.2\n")},                                   // 8 docker server version
		{result: stdoutResult("git version 2.43.0\n")},                       // 9 git --version
		{result: stdoutResult("directory\n")},                                // 10 stat path0
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 11 findmnt path0
		{result: stdoutResult("directory\n")},                                // 12 stat path1
		{result: stdoutResult("ext4 /dev/vda1\n")},                           // 13 findmnt path1
		{result: exitResult(1, "", "")},                                      // 14 findmnt host-shares
	}}
	a := New(fr)

	rep, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	if err != nil {
		t.Fatalf("Bootstrap: unexpected error: %v", err)
	}
	if fr.callCount() != 15 {
		t.Fatalf("callCount = %d, want 15 (usermod + ln reconcile steps present)", fr.callCount())
	}
	// usermod is the narrow group repair, additive and idempotent.
	if got, want := fr.callArgs(2), []string{"shell", "--tty=false", InstanceName, "--", "sudo", "-n", "usermod", "-aG", dockerGroup, HermesUser}; !equalArgs(got, want) {
		t.Fatalf("usermod argv = %v, want %v", got, want)
	}
	// The shim is a fixed symlink from the secure_path into the pinned launcher.
	if got, want := fr.callArgs(5), []string{"shell", "--tty=false", InstanceName, "--", "sudo", "-n", "ln", "-sfn", hermesTarget, hermesShimPath}; !equalArgs(got, want) {
		t.Fatalf("ln argv = %v, want %v", got, want)
	}
	if !checkOK(rep, "docker_group") || !checkOK(rep, "hermes_shim") {
		t.Fatalf("reconcile checks should be OK after repair: %+v", rep.Checks)
	}
}

func TestBootstrapMissingHermesInstallIsDrift(t *testing.T) {
	// The pinned launcher is absent (test -x fails): bootstrap must report drift
	// and must NOT create a dangling shim.
	s := bootstrapHappyScript()
	s[2] = scriptedResponse{result: exitResult(1, "", "")} // test -x target → missing
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapArchMismatchFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[4] = scriptedResponse{result: stdoutResult("x86_64\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHermesVersionNonZeroExitFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[5] = scriptedResponse{result: exitResult(127, "", "hermes: command not found")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHermesVersionZeroExitButEmptyFailsClosed(t *testing.T) {
	// A clean exit is not proof: empty/unrecognized version output is unverifiable.
	s := bootstrapHappyScript()
	s[5] = scriptedResponse{result: exitResult(0, "", "")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHermesPinnedVersionMismatchIsDrift(t *testing.T) {
	s := bootstrapHappyScript()
	s[5] = scriptedResponse{result: stdoutResult("Hermes Agent v0.19.0 (2026.7.20)\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{HermesVersion: "0.20.0"})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapDockerUnreachableFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[6] = scriptedResponse{result: exitResult(1, "", "Cannot connect to the Docker daemon")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapPathNotDirectoryFailsClosed(t *testing.T) {
	s := bootstrapHappyScript()
	s[8] = scriptedResponse{result: stdoutResult("regular file\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapPathNotNativeFilesystemFailsClosed(t *testing.T) {
	// A KB path backed by a macOS host share (virtiofs) violates ADR-0003.
	s := bootstrapHappyScript()
	s[9] = scriptedResponse{result: stdoutResult("virtiofs virtiofs-share\n")}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapHostMountPresentFailsClosed(t *testing.T) {
	// A broad macOS host mount must be detected and fail closed.
	s := bootstrapHappyScript()
	s[12] = scriptedResponse{result: stdoutResult("/home/hermes virtiofs\n")}
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
		// findmnt missing / exec failure: empty stdout but exit 127. Previously this
		// slipped through as "no_host_mounts: none"; it must now fail closed.
		{"exit127_empty", exitResult(127, "", "findmnt: command not found\n")},
		// Unexpected exit 0 with empty output is not the documented PASS shape.
		{"exit0_empty", exitResult(0, "", "")},
		// Some other query failure with no output must also fail closed.
		{"exit32_empty", exitResult(32, "", "findmnt: error\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := bootstrapHappyScript()
			s[12] = scriptedResponse{result: tc.result}
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
	s[4] = scriptedResponse{result: execx.Result{ExitCode: 0, Stdout: []byte("aarch64\n"), StdoutTruncated: true}}
	fr := &fakeRunner{script: s}
	a := New(fr)

	_, err := a.Bootstrap(context.Background(), BootstrapOptions{})
	assertKind(t, err, KindVerificationFailed)
}

func TestBootstrapTransportTimeoutIsExternal(t *testing.T) {
	// A transport failure (context deadline) is NOT a verification failure: it is
	// the adapter's existing timeout classification, mapped to the external class.
	fr := &fakeRunner{
		script: []scriptedResponse{
			{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))}, // list ok
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
		// Only the first (list) call matters for propagation; return a
		// not-found so Bootstrap stops immediately after.
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
