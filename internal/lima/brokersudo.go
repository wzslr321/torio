package lima

import "strings"

// How a `sudo -l -U <user>` answer is read.
//
// The exit code carries nothing. Asked by a caller that already holds root, sudo
// exits 0 whether the named identity may run everything or nothing, so a check
// keyed on it reports the same verdict for the two cases it exists to tell
// apart. Measured on a Lima guest, Ubuntu 24.04 with sudo 1.9.15p5: `sudo -n -l
// -U torio-mcp`, an identity with no sudo at all, exits 0, and so does the same
// question asked about root.
//
// So the answer is the sentence. It is matched positively in both directions,
// and anything else is unprovable rather than a no: silence is what a truncated,
// redirected or reworded sudo produces, and reading it as a denial is how a
// custody proof comes to pass on a guest it can no longer see.
//
// ADR-0009 records this for the backend identity and
// internal/backend/claudecode/isolation.go carries the same reading. The broker
// checks were written against the exit code and did not.
type sudoVerdictKind int

const (
	// sudoUnprovable is the safe default: the question was not answered in a
	// way this can act on.
	sudoUnprovable sudoVerdictKind = iota
	// sudoAbsent is the documented denial sentence.
	sudoAbsent
	// sudoPresent is the documented grant sentence.
	sudoPresent
)

// The two sentences sudo prints in the C locale. Every caller pins LC_ALL=C, so
// a guest's locale cannot decide whether a custody proof parses.
const (
	sudoDeniedPhrase  = "is not allowed to run sudo"
	sudoGrantedPhrase = "may run the following commands"
)

func sudoVerdict(res result) sudoVerdictKind {
	// A non-zero exit means the question was not answered, which is not the
	// same as answered no.
	if res.exit != 0 {
		return sudoUnprovable
	}
	switch {
	case strings.Contains(res.out, sudoDeniedPhrase):
		return sudoAbsent
	case strings.Contains(res.out, sudoGrantedPhrase):
		return sudoPresent
	default:
		return sudoUnprovable
	}
}

// sudoProbeArgv asks about an identity, in a pinned locale. It asks *about* the
// user rather than *as* the user on purpose: asked as the identity itself, sudo
// exits 1 saying a password is required, which is the same 1 a password-gated
// grant produces, so that form reports OK for exactly the identity this is
// meant to catch.
func sudoProbeArgv(user string) []string {
	return []string{"env", "LC_ALL=C", "sudo", "-n", "-l", "-U", user}
}
