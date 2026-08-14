package codex

import (
	"context"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

// TestVersionParserRequiresThisProgramsOwnShape pins the marker and its
// position. Codex prints "codex-cli <semver>", the reverse of the other
// backend, so a parser shared between them would have to accept both orders and
// would then accept either program's output as evidence about the other.
func TestVersionParserRequiresThisProgramsOwnShape(t *testing.T) {
	t.Run("the released shape parses", func(t *testing.T) {
		version, ok := parseVersion("codex-cli 0.147.0\n")
		if !ok {
			t.Fatal("the shape a released binary printed did not parse")
		}
		if version != "0.147.0" {
			t.Errorf("parsed %q, want the version and not the marker", version)
		}
	})

	for _, bad := range []string{
		"",
		"0.147.0\n",                // no marker at all
		"2.1.220 (Claude Code)\n",  // the other backend's output
		"codex-cli\n",              // marker with no version
		"some-other-cli 0.147.0\n", // a different program
		"0.147.0 codex-cli\n",      // right words, wrong order
	} {
		t.Run("rejected: "+strings.TrimSpace(bad), func(t *testing.T) {
			if _, ok := parseVersion(bad); ok {
				t.Errorf("%q was read as evidence about codex", bad)
			}
		})
	}
}

// TestPinnedDigestsCoverEveryArchitectureTheGuestCanBe pins the compile-time
// custody story. The release publishes no checksum for the archive carrying the
// agent binary (ADR-0022 P1), so the digest is committed here instead. A guest
// architecture with no digest must fail closed rather than install unverified
// bytes.
func TestPinnedDigestsCoverEveryArchitectureTheGuestCanBe(t *testing.T) {
	for _, triple := range []string{"x86_64-unknown-linux-musl", "aarch64-unknown-linux-musl"} {
		digest, ok := pinnedDigests[triple]
		if !ok {
			t.Fatalf("no pinned digest for %s, which a supported guest can be", triple)
		}
		if !isHexDigest(digest) {
			t.Errorf("the pinned digest for %s is not a sha256 digest: %q", triple, digest)
		}
	}
	if len(pinnedDigests) != 2 {
		t.Errorf("pinnedDigests carries %d entries; every one of them is a claim about a file", len(pinnedDigests))
	}
}

// TestUnsupportedArchitectureFailsClosed pins that an architecture nobody
// pinned a digest for stops the install, rather than falling through to a
// download nothing can verify.
func TestUnsupportedArchitectureFailsClosed(t *testing.T) {
	r := newFakeRunner(map[string]execx.Result{"uname -m": out("riscv64\n")})
	if _, _, err := guestTarget(context.Background(), r, "codex_install"); err == nil {
		t.Fatal("an unpinned architecture was allowed to proceed to a download")
	}
	if !strings.Contains(r.failed, "riscv64") {
		t.Errorf("the failure does not name the architecture: %q", r.failed)
	}
}

// installProbes is a guest where every step of a first install succeeds. Tests
// below take this and break exactly one answer, so what each assertion proves is
// the step it broke and not the setup.
func installProbes() map[string]execx.Result {
	target := targetPath()
	extracted := extractDir + "/codex-x86_64-unknown-linux-musl"
	return map[string]execx.Result{
		"uname -m":                     out("x86_64\n"),
		"sudo -n test -x " + target:    exit(1),
		"sudo -n rm -rf " + extractDir: exit(0),
		"sudo -n install -d -o root -g root -m 0700 " + extractDir:                                exit(0),
		"sudo -n -- curl -fsSL -o " + stagingPath + " " + archiveURL("x86_64-unknown-linux-musl"): exit(0),
		"sudo -n -- sha256sum --strict --check -":                                                 exit(0),
		"sudo -n -- tar -xzf " + stagingPath + " -C " + extractDir:                                exit(0),
		"sudo -n install -o root -g root -m 0755 " + extracted + " " + target:                     exit(0),
		"sudo -n rm -f " + stagingPath:                                                            exit(0),
		"sudo -n stat -c %F " + target:                                                            out("regular file\n"),
		"sudo -n stat -c %U:%G %a " + target:                                                      out("root:root 755\n"),
		"readlink " + commandPath:                                                                 out(target + "\n"),
	}
}

// TestInstallVerifiesBeforeItExecutes walks the whole first install and pins the
// order that makes it safe: the archive is checked against the committed digest
// before anything is extracted, and the digest travels on standard input rather
// than as an argv element.
func TestInstallVerifiesBeforeItExecutes(t *testing.T) {
	r := newFakeRunner(installProbes())
	if err := New().Install(context.Background(), r); err != nil {
		t.Fatalf("Install on a clean guest: %v", err)
	}

	checked, extracted := -1, -1
	for i, c := range r.calls {
		if strings.Contains(c, "sha256sum") {
			checked = i
		}
		if strings.Contains(c, "tar -xzf") {
			extracted = i
		}
	}
	if checked < 0 || extracted < 0 {
		t.Fatalf("install did not both verify and extract: %v", r.calls)
	}
	if checked > extracted {
		t.Error("the archive was extracted before its digest was checked")
	}

	wantDigest := pinnedDigests["x86_64-unknown-linux-musl"]
	var fedDigest bool
	for _, in := range r.stdin {
		if strings.Contains(in, wantDigest) {
			fedDigest = true
		}
	}
	if !fedDigest {
		t.Error("the expected digest never reached sha256sum on standard input")
	}
	for _, c := range r.calls {
		if strings.Contains(c, "sha256sum") && strings.Contains(c, wantDigest) {
			t.Error("the digest was interpolated into an argv rather than fed as input")
		}
	}
	if got := r.records["codex_install"]; !strings.Contains(got, PromotedVersion) {
		t.Errorf("a passing install recorded %q, want the version it pinned", got)
	}
}

// TestAMismatchedArchiveIsRemovedAndNeverExtracted is the case the pin exists
// for. Bytes that fail the digest must not survive the run, or a later one finds
// something archive-shaped already staged.
func TestAMismatchedArchiveIsRemovedAndNeverExtracted(t *testing.T) {
	probes := installProbes()
	probes["sudo -n -- sha256sum --strict --check -"] = exit(1)
	r := newFakeRunner(probes)

	if err := New().Install(context.Background(), r); err == nil {
		t.Fatal("an archive that failed its digest was installed")
	}
	if r.saw("tar -xzf") {
		t.Error("an unverified archive was extracted")
	}
	if !r.saw("rm -f " + stagingPath) {
		t.Error("the unverified bytes were left on the guest")
	}
}

// TestStatusDoesNotInstall pins the separation `Reconcile` exists for: a caller
// asking what is true must not download a binary or repoint a link on the way to
// answering.
func TestStatusDoesNotInstall(t *testing.T) {
	probes := installProbes()
	r := newFakeRunner(probes)
	r.repairs = false

	if err := New().Install(context.Background(), r); err == nil {
		t.Fatal("a read-only run installed the missing binary")
	}
	if r.saw("curl") {
		t.Error("a read-only run downloaded a binary")
	}
	if !strings.Contains(r.remediation, "bootstrap") {
		t.Errorf("the failure does not tell the operator what to run: %q", r.remediation)
	}
}

// TestAnAgentOwnedBinaryIsNotAPin pins the property the whole install exists to
// establish. The command an operator later runs under sudo must not be a file
// the agent can rewrite first.
func TestAnAgentOwnedBinaryIsNotAPin(t *testing.T) {
	for _, tc := range []struct {
		name string
		stat string
	}{
		{"owned by the agent", "codex:codex 755\n"},
		{"group-writable", "root:root 775\n"},
		{"world-writable", "root:root 757\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probes := installProbes()
			probes["sudo -n test -x "+targetPath()] = exit(0)
			probes["sudo -n stat -c %U:%G %a "+targetPath()] = out(tc.stat)
			r := newFakeRunner(probes)

			if err := New().Install(context.Background(), r); err == nil {
				t.Fatalf("%s passed as a pinned install", tc.name)
			}
		})
	}

	t.Run("a symlink is not a regular file", func(t *testing.T) {
		probes := installProbes()
		probes["sudo -n test -x "+targetPath()] = exit(0)
		probes["sudo -n stat -c %F "+targetPath()] = out("symbolic link\n")
		r := newFakeRunner(probes)
		if err := New().Install(context.Background(), r); err == nil {
			t.Fatal("a symlink passed as the pinned binary")
		}
	})
}

// TestVersionDriftIsReportedNotAccepted pins equality against the pin. "The
// output mentioned a version" is a weaker claim than it looks, and a box whose
// agent version moved underneath it cannot be reasoned about after the fact.
func TestVersionDriftIsReportedNotAccepted(t *testing.T) {
	probe := "sudo -n -u " + User + " -H -- codex --version"

	t.Run("the pinned version passes", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out("codex-cli " + PromotedVersion + "\n")})
		if err := New().VerifyVersion(context.Background(), r); err != nil {
			t.Fatalf("VerifyVersion: %v", err)
		}
		if got := r.records[versionCheck]; got != PromotedVersion {
			t.Errorf("recorded %q, want the version the renderer prints", got)
		}
	})

	t.Run("a different version fails", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out("codex-cli 0.148.0\n")})
		if err := New().VerifyVersion(context.Background(), r); err == nil {
			t.Fatal("a drifted version was accepted")
		}
	})

	t.Run("a clean exit is not proof", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out("")})
		if err := New().VerifyVersion(context.Background(), r); err == nil {
			t.Fatal("empty output was read as a version")
		}
	})

	t.Run("the run's own pin is honoured too", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out("codex-cli " + PromotedVersion + "\n")})
		r.pinned = "0.140.0"
		if err := New().VerifyVersion(context.Background(), r); err == nil {
			t.Fatal("a run pinned to another version accepted the built-in pin")
		}
	})
}
