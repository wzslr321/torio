package projects

import (
	"context"
	"regexp"
	"strings"

	"github.com/wzslr321/torio/internal/guestexec"
)

// Transport is how Git would reach the origin for a push.
//
// It is the answer to a question the mediated agent makes newly load-bearing: a
// forwarded SSH agent is used by the SSH transport and by nothing else, so a
// checkout that pushes over HTTPS gets no benefit from a pinned key and no
// dialog will ever appear for it.
type Transport string

const (
	TransportSSH   Transport = "ssh"
	TransportHTTPS Transport = "https"
	TransportOther Transport = "other"
)

// SessionIdentity selects whose known_hosts a check reads. The two session
// shapes run as different users with different home directories, and a host key
// trusted by one of them is not trusted by the other — which is exactly the way
// this was first discovered, twice, by hand.
type SessionIdentity int

const (
	// OperatorIdentity is the Lima login user, who runs `project shell`.
	OperatorIdentity SessionIdentity = iota
	// AgentIdentity is the backend's own uid, which runs a granted session.
	AgentIdentity
)

// RemoteAccess is what a session would find if it tried to reach the origin.
//
// It carries a verdict and a hostname, never the push URL. A push URL is not the
// registered remote — it can be set per checkout and nothing validated it — so
// it may carry a token, and this package does not put one in a report, an error
// or a terminal.
type RemoteAccess struct {
	// Transport is how Git would reach the origin for a push.
	Transport Transport
	// Host is the SSH host a push would contact, empty for every other
	// transport. It is safe to show: it is a hostname, and a hostname is what
	// the operator needs to fix a missing host key.
	Host string
	// HostKnown reports that the identity this was checked for already trusts
	// that host's key. False when the transport is not SSH, when the check could
	// not be made, and when the key is genuinely absent — all three mean a push
	// would stop, and none of them means it would succeed.
	HostKnown bool
}

// hostPattern is what may be passed to ssh-keygen as a hostname. It is narrow on
// purpose: the value is read out of a per-checkout Git config that nothing
// validated, and it becomes an argument to a guest command.
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,253}[A-Za-z0-9])?$`)

// RemoteAccess reports how a session would reach the origin, and whether the
// identity that would open it already trusts the host key.
//
// It is a separate call rather than part of a preflight because the answer
// depends on who is asking: `project shell` runs as the operator, a granted
// agent session runs as the backend's uid, and each has its own known_hosts. A
// preflight that answered for one of them would be wrong for the other in a way
// nothing would notice until a push failed.
func (m *Manager) RemoteAccess(ctx context.Context, id string, who SessionIdentity) (RemoteAccess, error) {
	const op = "remote_access"
	_, workspace, err := m.resolve(op, id)
	if err != nil {
		return RemoteAccess{}, err
	}

	// --push is the point: a checkout may push somewhere it does not fetch from,
	// and the push URL is the one a forwarded agent would be used for.
	res, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser,
		"git", "-C", workspace, "remote", "get-url", "--push", "origin"))
	if err != nil {
		return RemoteAccess{}, err
	}
	if res.ExitCode != 0 {
		return RemoteAccess{}, commandError(op, KindGit, "read the origin push URL", res.ExitCode)
	}

	access := classifyRemote(strings.TrimSpace(string(res.Stdout)))
	if access.Transport != TransportSSH || access.Host == "" {
		return access, nil
	}

	user := m.bootstrapOpts.OperatorUser
	if who == AgentIdentity {
		user = m.identity().GuestUser
	}
	if user == "" {
		return access, nil
	}
	// `ssh-keygen -F` exits 0 when the host is in known_hosts and 1 when it is
	// not. Any other exit is unverifiable state, and unverifiable is not known.
	known, err := m.run(ctx, op, guestexec.UserExecAs(user, "ssh-keygen", "-F", access.Host))
	if err != nil {
		return RemoteAccess{}, err
	}
	access.HostKnown = known.ExitCode == 0
	return access, nil
}

// classifyRemote reduces a Git remote URL to a transport and, for SSH, a host.
//
// The URL itself never leaves this function. Everything returned is either a
// fixed constant or a hostname that matched hostPattern, so nothing a
// per-checkout config carried can reach a caller.
func classifyRemote(raw string) RemoteAccess {
	if raw == "" {
		return RemoteAccess{Transport: TransportOther}
	}

	if scheme, rest, ok := strings.Cut(raw, "://"); ok {
		switch strings.ToLower(scheme) {
		case "ssh":
			return sshAccess(hostFromAuthority(rest))
		case "http", "https":
			return RemoteAccess{Transport: TransportHTTPS}
		default:
			return RemoteAccess{Transport: TransportOther}
		}
	}

	// The scp-like form, `[user@]host:path`. A colon after the first slash is
	// part of a local path, not a host separator.
	colon := strings.Index(raw, ":")
	slash := strings.Index(raw, "/")
	if colon > 0 && (slash < 0 || colon < slash) {
		return sshAccess(hostFromAuthority(raw[:colon]))
	}
	return RemoteAccess{Transport: TransportOther}
}

// hostFromAuthority strips the userinfo, the path and any port, leaving the
// host. An authority it cannot reduce to a plain hostname yields the empty
// string, and the caller treats that as a host it cannot vouch for.
func hostFromAuthority(authority string) string {
	if slash := strings.Index(authority, "/"); slash >= 0 {
		authority = authority[:slash]
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	// A bracketed IPv6 literal is a host Torio will not vouch for: known_hosts
	// records it in a form this would have to reproduce exactly, and guessing at
	// that is worse than saying so.
	if strings.HasPrefix(authority, "[") {
		return ""
	}
	if colon := strings.LastIndex(authority, ":"); colon >= 0 {
		if isDigits(authority[colon+1:]) {
			authority = authority[:colon]
		}
	}
	if !hostPattern.MatchString(authority) {
		return ""
	}
	return authority
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func sshAccess(host string) RemoteAccess {
	return RemoteAccess{Transport: TransportSSH, Host: host}
}
