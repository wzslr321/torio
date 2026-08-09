package sshagent

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// maxSocketPath is the practical limit on a Unix socket path. Darwin's
// sockaddr_un holds 104 bytes including the terminator, and a path that
// overflows it fails at bind with an error that names neither the limit nor the
// path. Refusing early says which it was.
const maxSocketPath = 103

// UpstreamFromEnv returns a dial function for the operator's own agent.
//
// It resolves SSH_AUTH_SOCK once, at the moment the session is being set up, and
// dials it per connection afterwards. The value is never logged and never
// reaches the guest: what the guest is given is the proxy's socket, and the two
// are deliberately different paths.
func UpstreamFromEnv() (func() (net.Conn, error), error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, errors.New("SSH_AUTH_SOCK is not set; start an ssh-agent on this Mac, then `ssh-add` the key that can push")
	}
	return func() (net.Conn, error) {
		return net.Dial("unix", socket)
	}, nil
}

// PinIdentity chooses the single key the proxy will offer.
//
// want is a fingerprint or a key comment, and it is required. An empty want
// would leave this function to choose a key on the operator's behalf —
// mediation by default over the sole loaded key, which is the alternative
// ADR-0015 rejects. A caller with nothing pinned does not mediate at all;
// one that reaches here anyway is refused before the agent is even asked.
func PinIdentity(dial func() (net.Conn, error), want string) (Identity, error) {
	if want == "" {
		return Identity{}, errors.New("no key is pinned; set operator_key to the fingerprint or comment of the key a session may use")
	}
	ids, err := listIdentities(dial)
	if err != nil {
		return Identity{}, err
	}
	if len(ids) == 0 {
		return Identity{}, errors.New("the SSH agent holds no identity to forward; `ssh-add` the key that can push")
	}

	var matched []Identity
	for _, id := range ids {
		if id.Fingerprint() == want || id.Comment == want {
			matched = append(matched, id)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return Identity{}, fmt.Errorf(
			"no identity in the SSH agent matches the pinned key; it holds: %s",
			strings.Join(fingerprints(ids), ", "))
	default:
		// Only a comment can match twice, and a comment that names two keys
		// names neither. The remedy is a fingerprint, so the error asks for one.
		return Identity{}, fmt.Errorf(
			"the pinned key matches %d identities; pin a fingerprint instead: %s",
			len(matched), strings.Join(fingerprints(matched), ", "))
	}
}

func listIdentities(dial func() (net.Conn, error)) ([]Identity, error) {
	conn, err := dial()
	if err != nil {
		// The cause is dropped rather than wrapped, as it is in the projects
		// preflight: this is the one diagnostic derived from talking to an
		// agent, and agent traffic is where key material would be if it were
		// anywhere.
		return nil, errors.New("the SSH agent at SSH_AUTH_SOCK could not be queried; confirm it is running")
	}
	defer conn.Close()

	answer, err := roundTrip(conn, frame{typ: msgRequestIdentities})
	if err != nil {
		return nil, errors.New("the SSH agent at SSH_AUTH_SOCK could not be queried; confirm it is running")
	}
	if answer.typ != msgIdentitiesAnswer {
		return nil, errors.New("the SSH agent at SSH_AUTH_SOCK refused to list its identities")
	}
	ids, err := parseIdentities(answer.body)
	if err != nil {
		return nil, errors.New("the SSH agent at SSH_AUTH_SOCK returned an identity list Torio could not read")
	}
	return ids, nil
}

func fingerprints(ids []Identity) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.Fingerprint())
	}
	return out
}

// Socket is the listening socket a session's proxy is reached on, together with
// the private directory that is the actual access control: socket modes are not
// honoured uniformly across platforms, a directory mode is.
type Socket struct {
	Path     string
	Listener net.Listener
	dir      string
}

// Listen creates a fresh private directory under root and binds inside it.
//
// The directory is per session and is removed with the socket, so a path never
// outlives the capability it named and a stale socket from a crashed session is
// never rebound.
func Listen(root string) (*Socket, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create the agent socket root: %w", err)
	}
	dir, err := os.MkdirTemp(root, "session-")
	if err != nil {
		return nil, fmt.Errorf("create the agent socket directory: %w", err)
	}
	path := filepath.Join(dir, "agent.sock")
	if len(path) > maxSocketPath {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("the agent socket path would be %d bytes, over the %d-byte limit", len(path), maxSocketPath)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("bind the agent socket: %w", err)
	}
	return &Socket{Path: path, Listener: listener, dir: dir}, nil
}

// Close ends the capability: the listener stops accepting and the directory that
// held the socket is removed.
func (s *Socket) Close() error {
	if s == nil {
		return nil
	}
	err := s.Listener.Close()
	if rmErr := os.RemoveAll(s.dir); err == nil {
		err = rmErr
	}
	return err
}
