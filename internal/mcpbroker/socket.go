package mcpbroker

import "path/filepath"

// SocketDir is where the broker publishes one socket per service (ADR-0004 §3).
// It is fixed here, beside ValidateServiceName, because the broker and the
// relay own opposite halves of one address: the broker decides what it binds,
// the relay what may be asked for. An overridable base — or a second copy of
// the rule — would let the two halves drift into a socket one side creates and
// the other cannot reach.
const SocketDir = "/run/torio-mcp"

// SocketSuffix keeps the service name and the file name distinct, so a name is
// never mistaken for a whole path.
const SocketSuffix = ".sock"

// SocketPath resolves the broker socket for one service under base. base is a
// parameter so tests can bind under a temp directory; production passes
// SocketDir.
//
// The containment is structural, not corrective: the name is checked against
// the service rule and rejected if it does not match, so no traversal,
// separator or absolute path can survive to be joined. There is deliberately
// no cleanup step — a caller that meant "atlassian" and wrote "Atlassian" must
// be told, not guessed at. ValidateServiceName's length bound is also what
// keeps the longest resolvable path inside the kernel's ~104-byte sun_path
// limit.
func SocketPath(base, service string) (string, error) {
	if err := ValidateServiceName(service); err != nil {
		return "", err
	}
	return filepath.Join(base, service+SocketSuffix), nil
}
