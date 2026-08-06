package lima

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
)

// Profile is the host-derived half of the trusted instance pins (ADR-0002).
//
// Four properties of the managed VM are pinned: the hypervisor driver, the
// guest architecture, and the guest image's URL and digest. They were four
// string literals for as long as exactly one host platform was supported, and
// a literal was the right shape for that.
//
// They are not an isolation guarantee. Both drivers below are hardware
// virtualization, and the threat model does not admit an adversarial agent.
// What they are is drift detection: together they answer one question — "is
// this instance the one Torio would have created?" — which is only meaningful
// against a single expected answer. Supporting a second host does not weaken
// that question; it makes the expected answer depend on the host asking it.
//
// This table is deliberately the only source for both sides of that question.
// renderTemplate writes these values into the template it hands to `limactl
// create`, and verifyCompatibleConfig compares Lima's report against the same
// struct. A pin the renderer writes but the verifier does not check — or the
// reverse — is the drift this shape exists to make unrepresentable.
type Profile struct {
	// Host is the "GOOS/GOARCH" this profile serves. It appears in errors so an
	// unsupported host is told what it is, not only that it is unsupported.
	Host string

	// VMType is Lima's driver. `vz` is Apple's Virtualization framework and
	// exists only on macOS; `qemu` is the Linux driver and runs over KVM.
	VMType string

	// Arch is both what Lima records in its instance config and what `uname -m`
	// prints inside the guest. Lima and the kernel agree on these spellings, so
	// one field serves the config check and the guest probe. Confirmed on both
	// platforms rather than assumed: a live x86_64 guest reported
	// `reported_arch=x86_64` alongside `guest_arch=x86_64`.
	Arch string

	// ImageURL and ImageDigest pin the guest image built for Arch. Both
	// architectures are the same Ubuntu build, so a guest differs from its
	// counterpart in machine code and in nothing else.
	ImageURL    string
	ImageDigest string

	// GuestGOARCH names the MCP payloads shipped beside the host CLI. The guest
	// always runs the host's architecture, so one release archive carries
	// exactly one pair of guest binaries and never has to choose between them.
	GuestGOARCH string
}

// promotedImageRelease is the Ubuntu build both profiles pin. Naming it once
// keeps the two URLs from drifting to different releases, which would make the
// two supported hosts run measurably different guests while both passing every
// check in this package.
const promotedImageRelease = "https://cloud-images.ubuntu.com/releases/noble/release-20260705/"

// profiles is the supported host matrix.
//
// macOS on Apple Silicon is where Torio started and remains the platform the
// release archive targets. Linux on x86_64 is supported because that is what
// most Linux workstations and every hosted CI runner are; it is also the only
// configuration in which an automated job can boot a guest at all, since hosted
// macOS runners report `kern.hv_support = 0` and cannot nest.
//
// Intel Macs are absent on purpose: `vz` requires Apple Silicon, and a
// darwin/amd64 host would have to run an emulated guest.
//
// arm64 Linux is absent because it is unproven here, not because it is
// impossible. Adding it is one row plus the arm64 digest already named below —
// but a row nothing has ever booted is a claim, and this table is read as a
// guarantee.
var profiles = map[string]Profile{
	"darwin/arm64": {
		Host:        "darwin/arm64",
		VMType:      "vz",
		Arch:        "aarch64",
		ImageURL:    promotedImageRelease + "ubuntu-24.04-server-cloudimg-arm64.img",
		ImageDigest: "sha256:7df0201546f75b8bcc1044594c806c35749421ad3c9bc1be2a3ab806cfae39cc",
		GuestGOARCH: "arm64",
	},
	"linux/amd64": {
		Host:        "linux/amd64",
		VMType:      "qemu",
		Arch:        "x86_64",
		ImageURL:    promotedImageRelease + "ubuntu-24.04-server-cloudimg-amd64.img",
		ImageDigest: "sha256:ffe6203da54deeb6db5d2a98a83f9ec8e55f149d3f7ba622e1abe5fa966ee3d6",
		GuestGOARCH: "amd64",
	},
}

// ProfileFor returns the profile for a host, or an error naming what is
// supported. It takes the platform as arguments rather than reading the runtime
// so the whole matrix is reachable from a test on any machine — a check that
// only ever runs against the host it was written on proves the least where it
// matters most.
func ProfileFor(goos, goarch string) (Profile, error) {
	p, ok := profiles[goos+"/"+goarch]
	if !ok {
		return Profile{}, fmt.Errorf("unsupported host %s/%s; Torio supports %s", goos, goarch, SupportedHosts())
	}
	return p, nil
}

// HostProfile returns the profile for the machine this process runs on.
func HostProfile() (Profile, error) { return ProfileFor(runtime.GOOS, runtime.GOARCH) }

// SupportedHosts lists the matrix in a stable order, for error messages and
// for documentation that would otherwise restate it and fall behind.
func SupportedHosts() string {
	hosts := make([]string, 0, len(profiles))
	for h := range profiles {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	return strings.Join(hosts, ", ")
}

// MCPBrokerArtifact and MCPRelayArtifact name the guest payloads for this
// profile. They are derived rather than stored so a profile cannot name a
// broker for one architecture and a relay for another.
func (p Profile) MCPBrokerArtifact() string { return "torio-mcp-broker-linux-" + p.GuestGOARCH }
func (p Profile) MCPRelayArtifact() string  { return "torio-mcp-connect-linux-" + p.GuestGOARCH }

// valid reports whether the profile carries every pin. The zero Profile is not
// usable, and an Adapter constructed on an unsupported host holds exactly that,
// so every operation that depends on a pin fails closed instead of comparing
// against empty strings — which would otherwise accept any instance at all.
func (p Profile) valid() bool {
	return p.VMType != "" && p.Arch != "" && p.ImageURL != "" && p.ImageDigest != "" && p.GuestGOARCH != ""
}
