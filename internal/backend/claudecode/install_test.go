package claudecode

import (
	"strings"
	"testing"
)

const goodManifest = `{
  "version": "` + PromotedVersion + `",
  "platforms": {
    "linux-arm64": {"binary": "claude", "checksum": "3e50836e227868746273653e0f8115cf5fc9cb34a081847c6040c81d80812c33"},
    "linux-x64":   {"binary": "claude", "checksum": "a2b5add7dc4bcd8eaa029f4e8bdac4df7769b4073698db7989d206baf9419c2d"}
  }
}`

// TestManifestChecksumReadsThePublishedDigest is the happy path: the digest the
// download will be checked against comes from the vendor's own manifest for the
// pinned version, not from anything Torio carries.
func TestManifestChecksumReadsThePublishedDigest(t *testing.T) {
	got, err := manifestChecksum([]byte(goodManifest), "linux-arm64")
	if err != nil {
		t.Fatalf("manifestChecksum: %v", err)
	}
	const want = "3e50836e227868746273653e0f8115cf5fc9cb34a081847c6040c81d80812c33"
	if got != want {
		t.Fatalf("checksum = %q, want %q", got, want)
	}
}

// TestManifestChecksumFailsClosed pins every way a manifest can fail to answer
// the one question asked of it. A checksum Torio does not understand is worse
// than no checksum: it would be fed to `sha256sum --check`, whose failure is
// indistinguishable from the download being wrong.
func TestManifestChecksumFailsClosed(t *testing.T) {
	cases := []struct {
		name     string
		doc      string
		platform string
		wants    string
	}{
		{
			name:     "not JSON",
			doc:      "<html>404</html>",
			platform: "linux-arm64",
			wants:    "not readable JSON",
		},
		{
			name:     "a different version than the pin",
			doc:      strings.Replace(goodManifest, PromotedVersion, "9.9.9", 1),
			platform: "linux-arm64",
			wants:    "declares version",
		},
		{
			name:     "no entry for the platform",
			doc:      goodManifest,
			platform: "linux-riscv64",
			wants:    "publishes no checksum",
		},
		{
			name:     "a checksum that is not a digest",
			doc:      strings.Replace(goodManifest, "3e50836e227868746273653e0f8115cf5fc9cb34a081847c6040c81d80812c33", "not-a-digest", 1),
			platform: "linux-arm64",
			wants:    "not a sha256 digest",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := manifestChecksum([]byte(tc.doc), tc.platform)
			if err == nil {
				t.Fatal("manifestChecksum accepted a manifest it should have refused")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q does not explain the refusal (want %q)", err, tc.wants)
			}
		})
	}
}

// TestParseVersionRequiresTheProgramToIdentifyItself pins that a clean exit is
// not proof. Output that does not name the program is not evidence about the
// program, whatever it happens to contain.
func TestParseVersionRequiresTheProgramToIdentifyItself(t *testing.T) {
	if got, ok := parseVersion("2.1.220 (Claude Code)\n"); !ok || got != "2.1.220" {
		t.Fatalf("parseVersion = %q, %v; want %q, true", got, ok, "2.1.220")
	}
	for _, out := range []string{
		"",
		"2.1.220\n",
		"some other tool 2.1.220\n",
		"\n",
	} {
		if _, ok := parseVersion(out); ok {
			t.Errorf("parseVersion(%q) accepted output that does not identify Claude Code", out)
		}
	}
}

// TestIsHexDigestRejectsAnythingThatIsNotOne guards the value that decides
// whether downloaded bytes get executed.
func TestIsHexDigestRejectsAnythingThatIsNotOne(t *testing.T) {
	valid := strings.Repeat("a", 64)
	if !isHexDigest(valid) {
		t.Error("a 64-character lowercase hex string was rejected")
	}
	for _, bad := range []string{
		"",
		strings.Repeat("a", 63),
		strings.Repeat("a", 65),
		strings.Repeat("A", 64), // uppercase: sha256sum writes lowercase
		strings.Repeat("g", 64),
		strings.Repeat("a", 32) + " " + strings.Repeat("a", 31),
	} {
		if isHexDigest(bad) {
			t.Errorf("isHexDigest(%q) accepted a non-digest", bad)
		}
	}
}

// TestTheCommandPathPointsAtARootOwnedTarget pins the asymmetry with the Hermes
// shim that ADR-0009 records. The stable name on sudo's secure_path must resolve
// to a file under a root-owned system directory, never to something the agent's
// own uid could rewrite before an operator runs it.
func TestTheCommandPathPointsAtARootOwnedTarget(t *testing.T) {
	if !strings.HasPrefix(targetPath(), installDir+"/") {
		t.Fatalf("target %q is not under the root-owned install directory %q", targetPath(), installDir)
	}
	if strings.HasPrefix(targetPath(), Home) {
		t.Fatalf("target %q is under the agent's home; the agent could replace it", targetPath())
	}
	if !strings.Contains(targetPath(), PromotedVersion) {
		t.Errorf("target %q does not carry the pinned version, so a pin change would not move it", targetPath())
	}
}
