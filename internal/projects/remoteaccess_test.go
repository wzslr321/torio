package projects

import (
	"context"
	"strings"
	"testing"
)

func TestClassifyRemoteReadsTheTransportAndHost(t *testing.T) {
	for name, tc := range map[string]struct {
		raw       string
		transport Transport
		host      string
	}{
		"scp-like":         {"git@github.com:owner/repo.git", TransportSSH, "github.com"},
		"scp-like no user": {"github.com:owner/repo.git", TransportSSH, "github.com"},
		"ssh scheme":       {"ssh://git@github.com/owner/repo.git", TransportSSH, "github.com"},
		"ssh with port":    {"ssh://git@github.com:2222/owner/repo.git", TransportSSH, "github.com"},
		"https":            {"https://github.com/owner/repo.git", TransportHTTPS, ""},
		"http":             {"http://github.com/owner/repo.git", TransportHTTPS, ""},
		"git protocol":     {"git://github.com/owner/repo.git", TransportOther, ""},
		"local path":       {"/srv/git/repo.git", TransportOther, ""},
		"empty":            {"", TransportOther, ""},
	} {
		got := classifyRemote(tc.raw)
		if got.Transport != tc.transport || got.Host != tc.host {
			t.Errorf("%s: classifyRemote(%q) = %s/%q, want %s/%q", name, tc.raw, got.Transport, got.Host, tc.transport, tc.host)
		}
		if got.HostKnown {
			t.Errorf("%s: classifyRemote claimed a host key it never checked", name)
		}
	}
}

// TestClassifyRemoteNeverReturnsTheURL is the custody rule for this probe. A
// push URL is not the registered remote: it is set per checkout, nothing
// validated it, and it can carry a token. Only a constant and a matched hostname
// leave this function.
func TestClassifyRemoteNeverReturnsTheURL(t *testing.T) {
	const token = "not-a-real-token-0000"
	for _, raw := range []string{
		"https://user:" + token + "@github.com/owner/repo.git",
		"ssh://" + token + "@github.com/owner/repo.git",
		token + "@github.com:owner/repo.git",
	} {
		got := classifyRemote(raw)
		if strings.Contains(got.Host, token) || strings.Contains(string(got.Transport), token) {
			t.Errorf("classifyRemote(%q) leaked the credential: %+v", raw, got)
		}
	}
}

// TestClassifyRemoteRefusesAHostItCannotVouchFor keeps a value nothing validated
// from becoming an argument to a guest command.
func TestClassifyRemoteRefusesAHostItCannotVouchFor(t *testing.T) {
	for name, raw := range map[string]string{
		"shell syntax": "git@github.com;rm -rf /:owner/repo.git",
		"space":        "git@git hub.com:owner/repo.git",
		"leading dash": "git@-oProxyCommand=x:owner/repo.git",
		"ipv6 literal": "ssh://git@[2001:db8::1]/owner/repo.git",
		"empty host":   "git@:owner/repo.git",
	} {
		got := classifyRemote(raw)
		if got.Host != "" {
			t.Errorf("%s: classifyRemote(%q) returned host %q", name, raw, got.Host)
		}
	}
}

// TestRemoteAccessChecksTheIdentityThatWillUseIt is the bug this exists for. The
// two session shapes run as different users with different home directories, so
// a host key trusted by one is not trusted by the other.
func TestRemoteAccessChecksTheIdentityThatWillUseIt(t *testing.T) {
	for name, tc := range map[string]struct {
		who      SessionIdentity
		wantUser string
	}{
		"operator session": {OperatorIdentity, testOwner},
		"agent session":    {AgentIdentity, "hermes"},
	} {
		g := shellFake()
		g.pushURL = "git@github.com:owner/demo.git"
		g.knownHosts = map[string][]string{tc.wantUser: {"github.com"}}

		access, err := newTestManager(g, registryWith(testProject())).RemoteAccess(context.Background(), testID, tc.who)
		if err != nil {
			t.Fatalf("%s: RemoteAccess() error = %v", name, err)
		}
		if access.Transport != TransportSSH || access.Host != "github.com" {
			t.Fatalf("%s: access = %+v, want ssh/github.com", name, access)
		}
		if !access.HostKnown {
			t.Errorf("%s: host key was not looked for under %q", name, tc.wantUser)
		}

		// The same host, trusted by the other identity only, must not count.
		other := shellFake()
		other.pushURL = "git@github.com:owner/demo.git"
		other.knownHosts = map[string][]string{"somebody-else": {"github.com"}}
		access, err = newTestManager(other, registryWith(testProject())).RemoteAccess(context.Background(), testID, tc.who)
		if err != nil {
			t.Fatalf("%s: RemoteAccess() error = %v", name, err)
		}
		if access.HostKnown {
			t.Errorf("%s: a key trusted by another identity was counted as known", name)
		}
	}
}

// TestRemoteAccessSkipsTheHostCheckForNonSSH proves the check is not run where
// it would mean nothing: an HTTPS push never touches a host key or the agent.
func TestRemoteAccessSkipsTheHostCheckForNonSSH(t *testing.T) {
	g := shellFake()
	g.pushURL = "https://github.com/owner/demo.git"

	access, err := newTestManager(g, registryWith(testProject())).RemoteAccess(context.Background(), testID, OperatorIdentity)
	if err != nil {
		t.Fatalf("RemoteAccess() error = %v", err)
	}
	if access.Transport != TransportHTTPS || access.Host != "" || access.HostKnown {
		t.Errorf("access = %+v, want https with nothing claimed about a host key", access)
	}
	for _, call := range g.calls {
		if strings.Contains(strings.Join(call.argv, " "), "ssh-keygen") {
			t.Error("a host key was looked up for a transport that never uses one")
		}
	}
}
