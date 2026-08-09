package claudecode

import (
	"context"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

// The install as a sequence, rather than as the pieces install_test.go pins
// separately. What a unit test on manifestChecksum cannot say is whether a
// refused manifest stops the download, whether the checksum is consulted before
// the bytes are placed, and what happens to bytes that failed it.

const (
	presentProbe  = "sudo -n test -x " + installDir + "/claude-" + PromotedVersion
	archProbe     = "uname -m"
	manifestProbe = "sudo -n -- curl -fsSL " + downloadBaseURL + "/" + PromotedVersion + "/manifest.json"
	binaryProbe   = "sudo -n -- curl -fsSL -o " + stagingPath + " " + downloadBaseURL + "/" + PromotedVersion + "/linux-arm64/claude"
	checksumProbe = "sudo -n -- sha256sum --strict --check -"
	installProbe  = "sudo -n install -o root -g root -m 0755 " + stagingPath + " " + installDir + "/claude-" + PromotedVersion
	stagingRemove = "sudo -n rm -f " + stagingPath
	targetKind    = "sudo -n stat -c %F " + installDir + "/claude-" + PromotedVersion
	targetOG      = "sudo -n stat -c %U:%G %a " + installDir + "/claude-" + PromotedVersion
	linkProbe     = "readlink " + commandPath
)

// freshGuest is a box with no pinned binary yet, where every step of the
// download succeeds. Cases spoil one answer, so each reads as the one thing it
// is about.
func freshGuest() map[string]execx.Result {
	return map[string]execx.Result{
		presentProbe:  exit(1),
		archProbe:     out("aarch64\n"),
		manifestProbe: out(goodManifest),
		binaryProbe:   exit(0),
		checksumProbe: exit(0),
		installProbe:  exit(0),
		stagingRemove: exit(0),
		targetKind:    out("regular file\n"),
		targetOG:      out("root:root 755\n"),
		linkProbe:     out(installDir + "/claude-" + PromotedVersion + "\n"),
	}
}

func installerWith(overrides map[string]execx.Result) *fakeRunner {
	answers := freshGuest()
	for probe, result := range overrides {
		answers[probe] = result
	}
	return newFakeRunner(answers)
}

func indexOfCall(r *fakeRunner, want string) int {
	for i, c := range r.calls {
		if c == want {
			return i
		}
	}
	return -1
}

// TestInstallVerifiesBeforeItInstalls pins the order the whole pin depends on:
// the downloaded bytes are checked against the published digest, and only then
// placed where the agent's identity can later run them.
func TestInstallVerifiesBeforeItInstalls(t *testing.T) {
	r := installerWith(nil)
	if err := New().Install(context.Background(), r); err != nil {
		t.Fatalf("Install on a fresh guest: %v", err)
	}
	sum, install := indexOfCall(r, checksumProbe), indexOfCall(r, installProbe)
	if sum < 0 {
		t.Fatal("the downloaded bytes were never checked against the published digest")
	}
	if install < 0 {
		t.Fatal("a verified binary was never installed")
	}
	if sum > install {
		t.Errorf("the binary was installed before it was verified: checksum=%d install=%d", sum, install)
	}
}

// TestDownloadRefusesBytesThatDoNotMatchThePublishedChecksum is the
// supply-chain case. What matters is not only the refusal: the unverified bytes
// must be gone, because a staging file left behind is something a later run
// could adopt.
func TestDownloadRefusesBytesThatDoNotMatchThePublishedChecksum(t *testing.T) {
	r := installerWith(map[string]execx.Result{checksumProbe: exit(1)})

	if err := New().Install(context.Background(), r); err == nil {
		t.Fatal("bytes that do not match the published checksum were installed")
	}
	if !strings.Contains(r.failed, "checksum") {
		t.Errorf("failure does not name the checksum: %q", r.failed)
	}
	if !r.saw(stagingRemove) {
		t.Error("the unverified download was left on the guest")
	}
	if r.saw(installProbe) {
		t.Error("unverified bytes were installed anyway")
	}
}

// TestAManifestThatDoesNotAnswerStopsTheDownload covers what the unit test on
// manifestChecksum cannot reach: the fetch itself failing, and the consequence
// of any refusal, which is that nothing is downloaded on the strength of a
// document Torio would not read.
func TestAManifestThatDoesNotAnswerStopsTheDownload(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  execx.Result
		says string
	}{
		{"a fetch that failed", exit(22), "could not fetch"},
		{"a document that is not the manifest", out("<html>404</html>"), "readable JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := installerWith(map[string]execx.Result{manifestProbe: tc.doc})
			if err := New().Install(context.Background(), r); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(r.failed, tc.says) {
				t.Errorf("failure does not name the problem, want %q: %q", tc.says, r.failed)
			}
			if r.saw(binaryProbe) {
				t.Error("a binary was downloaded on the strength of a manifest that was refused")
			}
		})
	}
}

// TestVerifyVersionRequiresTheExactPinnedVersion drives the check rather than
// the parser install_test.go pins. Each case here returns some kind of success
// to the caller and must still be refused, which is the property that separates
// this check from running the command and seeing whether it crashed.
func TestVerifyVersionRequiresTheExactPinnedVersion(t *testing.T) {
	const probe = "sudo -n -u " + User + " -H -- claude --version"

	t.Run("the pinned version passes", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out(PromotedVersion + " " + versionMarker + "\n")})
		if err := New().VerifyVersion(context.Background(), r); err != nil {
			t.Fatalf("VerifyVersion on the pinned version: %v", err)
		}
		if r.records[versionCheck] != PromotedVersion {
			t.Errorf("recorded %q, want %q", r.records[versionCheck], PromotedVersion)
		}
	})

	for _, tc := range []struct {
		name   string
		result execx.Result
		says   string
	}{
		{"a command that exited non-zero", execx.Result{ExitCode: 127, Stdout: []byte(PromotedVersion + " " + versionMarker + "\n")}, "exited non-zero"},
		{"output that does not identify the program", out("2.1.220\n"), "no recognizable version"},
		{"another version of the same program", out("2.0.1 " + versionMarker + "\n"), "pinned"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFakeRunner(map[string]execx.Result{probe: tc.result})
			if err := New().VerifyVersion(context.Background(), r); err == nil {
				t.Fatalf("%s was read as proof of the pinned version", tc.name)
			}
			if !strings.Contains(r.failed, tc.says) {
				t.Errorf("failure does not name the problem, want %q: %q", tc.says, r.failed)
			}
		})
	}
}
