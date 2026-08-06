package lima

import (
	"strings"
	"testing"
)

// testProfile is the profile the adapter resolves on the machine running the
// tests. The suite's fake-runner fixtures are built from it, so a fixture
// always describes an instance this host would actually have created.
//
// The tests below deliberately do not use it: they walk the whole matrix by
// name, because a check that only ever runs against its own host proves the
// least about the platform nobody is sitting on.
var testProfile = mustHostProfile()

func mustHostProfile() Profile {
	p, err := HostProfile()
	if err != nil {
		panic("internal/lima tests need a supported Torio host: " + err.Error())
	}
	return p
}

func TestEverySupportedHostIsFullyPinned(t *testing.T) {
	for host, p := range profiles {
		if !p.valid() {
			t.Errorf("%s: incomplete profile %+v", host, p)
		}
		if p.Host != host {
			t.Errorf("%s: Host field is %q; a profile that misreports its own key sends an operator to the wrong row", host, p.Host)
		}
		if !strings.HasPrefix(p.ImageDigest, "sha256:") || len(p.ImageDigest) != len("sha256:")+64 {
			t.Errorf("%s: image digest %q is not a sha256 digest", host, p.ImageDigest)
		}
		if !strings.HasPrefix(p.ImageURL, "https://") {
			t.Errorf("%s: image URL %q is not https", host, p.ImageURL)
		}
	}
}

// The two hosts must differ in every pin that describes machine code. Sharing
// an image or an architecture between them would mean one of the two runs a
// guest built for the other, which is the mistake this table exists to prevent
// and which no single-host test can see.
func TestSupportedHostsDoNotShareGuestPins(t *testing.T) {
	darwin, err := ProfileFor("darwin", "arm64")
	if err != nil {
		t.Fatalf("darwin/arm64: %v", err)
	}
	linux, err := ProfileFor("linux", "amd64")
	if err != nil {
		t.Fatalf("linux/amd64: %v", err)
	}
	if darwin.Arch == linux.Arch {
		t.Errorf("both hosts claim arch %q", darwin.Arch)
	}
	if darwin.VMType == linux.VMType {
		t.Errorf("both hosts claim vmType %q", darwin.VMType)
	}
	if darwin.ImageURL == linux.ImageURL {
		t.Errorf("both hosts pin image %q", darwin.ImageURL)
	}
	if darwin.ImageDigest == linux.ImageDigest {
		t.Errorf("both hosts pin digest %q", darwin.ImageDigest)
	}
	if darwin.GuestGOARCH == linux.GuestGOARCH {
		t.Errorf("both hosts ship guest binaries for %q", darwin.GuestGOARCH)
	}
	// Same Ubuntu build, different machine. A digest bump that moved only one
	// host would leave the two supported platforms running different releases
	// while every other check still passed.
	if !strings.HasPrefix(darwin.ImageURL, promotedImageRelease) ||
		!strings.HasPrefix(linux.ImageURL, promotedImageRelease) {
		t.Errorf("hosts pin images from different Ubuntu releases: %q and %q", darwin.ImageURL, linux.ImageURL)
	}
}

func TestProfileForRejectsUnsupportedHosts(t *testing.T) {
	for _, tc := range []struct{ goos, goarch string }{
		{"darwin", "amd64"}, // Intel Mac: vz needs Apple Silicon
		{"linux", "arm64"},  // plausible, but unproven here
		{"windows", "amd64"},
		{"", ""},
	} {
		p, err := ProfileFor(tc.goos, tc.goarch)
		if err == nil {
			t.Errorf("%s/%s: want error, got profile %+v", tc.goos, tc.goarch, p)
			continue
		}
		if p.valid() {
			t.Errorf("%s/%s: rejected host returned a usable profile", tc.goos, tc.goarch)
		}
		// The operator must be told which host they are on and what would work.
		if !strings.Contains(err.Error(), tc.goos+"/"+tc.goarch) {
			t.Errorf("%s/%s: error does not name the host: %v", tc.goos, tc.goarch, err)
		}
		if !strings.Contains(err.Error(), "darwin/arm64") || !strings.Contains(err.Error(), "linux/amd64") {
			t.Errorf("%s/%s: error does not list the supported hosts: %v", tc.goos, tc.goarch, err)
		}
	}
}

func TestGuestArtifactNamesFollowTheGuestArchitecture(t *testing.T) {
	for host, p := range profiles {
		broker, relay := p.MCPBrokerArtifact(), p.MCPRelayArtifact()
		if !strings.HasSuffix(broker, "-linux-"+p.GuestGOARCH) {
			t.Errorf("%s: broker artifact %q does not name guest arch %q", host, broker, p.GuestGOARCH)
		}
		if !strings.HasSuffix(relay, "-linux-"+p.GuestGOARCH) {
			t.Errorf("%s: relay artifact %q does not name guest arch %q", host, relay, p.GuestGOARCH)
		}
		if broker == relay {
			t.Errorf("%s: broker and relay share the artifact name %q", host, broker)
		}
	}
}

// An Adapter on an unsupported host holds the zero Profile. Every operation
// that depends on a pin must refuse it, because comparing an instance against
// empty pins would accept any instance at all — the exact inversion of what
// the pins are for.
func TestZeroProfileFailsClosed(t *testing.T) {
	a := &Adapter{}
	if _, err := a.profile(); err == nil {
		t.Fatal("profile() accepted the zero Profile")
	}

	if _, err := renderTemplate(InitOptions{OperatorUser: "operator"}, Profile{}); err == nil {
		t.Error("renderTemplate accepted an incomplete profile")
	}

	rec := &instanceRecord{Name: InstanceName, Config: &instanceConfig{}}
	if err := verifyCompatibleConfig(rec, Profile{}); err == nil {
		t.Error("verifyCompatibleConfig accepted an empty instance against empty pins")
	}
}

// The renderer and the verifier must agree for every host, not only the one
// running the test. Rendering a template for a profile and reading the pins
// back out of it proves they read the same struct.
func TestRenderedTemplateCarriesEachHostsPins(t *testing.T) {
	for host, p := range profiles {
		body, err := renderTemplate(InitOptions{OperatorUser: "operator"}, p)
		if err != nil {
			t.Errorf("%s: renderTemplate: %v", host, err)
			continue
		}
		text := string(body)
		for _, want := range []string{
			"vmType: " + p.VMType,
			"arch: " + p.Arch,
			p.ImageURL,
			p.ImageDigest,
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: rendered template missing %q", host, want)
			}
		}
		if strings.Contains(text, "__TORIO_VMTYPE__") ||
			strings.Contains(text, "__TORIO_ARCH__") ||
			strings.Contains(text, "__TORIO_IMAGE_URL__") ||
			strings.Contains(text, "__TORIO_IMAGE_DIGEST__") {
			t.Errorf("%s: rendered template still carries a pin placeholder", host)
		}

		// The other host's pins must not appear. A replacer that rewrote the
		// wrong token would still render a valid-looking document.
		for other, q := range profiles {
			if other == host {
				continue
			}
			if strings.Contains(text, q.ImageDigest) {
				t.Errorf("%s: rendered template carries %s's image digest", host, other)
			}
		}
	}
}

// A template rendered for one host must not verify against another. This is
// the property the pins exist for, stated directly.
func TestInstanceOfOneHostDoesNotVerifyAgainstAnother(t *testing.T) {
	darwin, _ := ProfileFor("darwin", "arm64")
	linux, _ := ProfileFor("linux", "amd64")

	rec := instanceRecordFor(darwin)
	if err := verifyCompatibleConfig(rec, darwin); err != nil {
		t.Fatalf("darwin instance rejected by its own profile: %v", err)
	}
	if err := verifyCompatibleConfig(rec, linux); err == nil {
		t.Fatal("a darwin/arm64 instance verified against the linux/amd64 pins")
	}
}

func instanceRecordFor(p Profile) *instanceRecord {
	rec := &instanceRecord{
		Name: InstanceName,
		Config: &instanceConfig{
			VMType: p.VMType,
			Arch:   p.Arch,
			Images: []struct {
				Location string `json:"location"`
				Digest   string `json:"digest"`
			}{
				{Location: p.ImageURL, Digest: p.ImageDigest},
			},
		},
	}
	rec.Config.SSH.ForwardAgent = false
	return rec
}
