package claudecode

import (
	"strconv"
	"strings"
)

// The small parsers this package needs. They are local rather than shared
// because every one of them decides something a check then fails closed on, and
// a shared helper that grew a "lenient" mode for one caller would quietly
// change what the others prove.

func trimmed(out []byte) string { return strings.TrimSpace(string(out)) }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// hasGroup reports whether a space-separated `id -nG` list contains group.
func hasGroup(groups []byte, group string) bool {
	for _, g := range strings.Fields(string(groups)) {
		if g == group {
			return true
		}
	}
	return false
}

// parseOwnershipMode parses `stat -c '%U:%G %a'`. Unparseable input returns
// ok=false rather than empty strings a comparison might accidentally match.
func parseOwnershipMode(out []byte) (owner, group, mode string, ok bool) {
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) != 2 {
		return "", "", "", false
	}
	parts := strings.SplitN(fields[0], ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || fields[1] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], fields[1], true
}

// modeGrantsForeignWrite reports whether a `stat -c %a` mode lets anyone but
// the owner write, and whether the mode could be read at all. An unreadable
// mode is not proof of anything and its caller fails closed.
func modeGrantsForeignWrite(mode string) (writable, parsed bool) {
	bits, err := strconv.ParseUint(strings.TrimSpace(mode), 8, 32)
	if err != nil {
		return false, false
	}
	return bits&0o022 != 0, true
}

// isHexDigest reports whether s is a 64-character lowercase hex SHA-256. The
// value is fed to `sha256sum --check`, and a malformed one would make the check
// fail for the wrong reason — which is indistinguishable, from the outside,
// from the download being wrong.
func isHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
