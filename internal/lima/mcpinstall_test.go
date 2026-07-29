package lima

import (
	"context"
	"strings"
	"testing"
)

// freshInstallScript is a guest with nothing provisioned yet: every probe says
// absent, every mutation succeeds, and the closing identity verification then
// sees the finished shape.
func freshInstallScript() []scriptedResponse {
	return []scriptedResponse{
		{result: exitResult(2, "", "")},                     // getent group torio-mcp-clients -> absent
		{result: stdoutResult("")},                          // groupadd
		{result: exitResult(1, "", "no user")},              // id -u torio-mcp -> absent
		{result: stdoutResult("")},                          // useradd
		{result: stdoutResult("directory\n")},               // stat %F home (useradd made it)
		{result: stdoutResult("torio-mcp:torio-mcp 755\n")}, // stat %U:%G %a -> default mode, too open
		{result: stdoutResult("")},                          // chmod 700
		{result: stdoutResult("hermes torio-projects\n")},   // id -nG hermes -> not yet a client
		{result: stdoutResult("")},                          // usermod -aG
		{result: exitResult(1, "", "no such file")},         // stat %F policy dir -> absent
		{result: stdoutResult("")},                          // install -d policy dir
		// closing identity verification
		{result: stdoutResult("997\n")},
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},
		// Two lines: the verification probe names statControlPath first, so a
		// present path answers with the control line and its own.
		{result: stdoutResult("directory\ndirectory\n")},
		{result: stdoutResult("torio-mcp:torio-mcp 700\n")},
	}
}

// settledInstallScript is a guest that already holds the finished shape: every
// probe answers "present", so no mutation may be issued at all.
func settledInstallScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},          // getent group
		{result: stdoutResult("997\n")},                                     // id -u torio-mcp
		{result: stdoutResult("directory\n")},                               // stat %F home
		{result: stdoutResult("torio-mcp:torio-mcp 700\n")},                 // stat %U:%G %a home
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")}, // id -nG hermes
		{result: stdoutResult("directory\n")},                               // stat %F policy dir
		// closing identity verification
		{result: stdoutResult("997\n")},
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},
		{result: stdoutResult("directory\ndirectory\n")},
		{result: stdoutResult("torio-mcp:torio-mcp 700\n")},
	}
}

func TestInstallMCPBrokerProvisionsAndVerifies(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	a := New(fr)

	rep, err := a.InstallMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}
	if !rep.Changed {
		t.Error("a fresh guest was provisioned but Changed is false")
	}

	var joined []string
	for i := 0; i < fr.callCount(); i++ {
		joined = append(joined, strings.Join(fr.callArgs(i), " "))
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{
		"groupadd --system " + TorioMCPClientsGroup,
		"useradd --system",
		"--shell /usr/sbin/nologin",
		"usermod -aG " + TorioMCPClientsGroup + " " + HermesUser,
		"chmod 700 " + TorioMCPHome,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("install never issued %q; issued:\n%s", want, all)
		}
	}
}

// TestInstallMCPBrokerNeverGrantsBroaderAuthority is the check that keeps this
// installer from quietly becoming a privilege grant. torio-mcp must not join
// torio-projects (it would reach project checkouts) and hermes must not join
// torio-mcp (it would read the credential store) -- the two mistakes that would
// void ADR-0022 while leaving every other check green.
func TestInstallMCPBrokerNeverGrantsBroaderAuthority(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	a := New(fr)

	if _, err := a.InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}
	// Only the value of a group flag counts. Matching loose substrings would be
	// worse than no check at all here: "torio-mcp-clients" has "torio-mcp" as a
	// prefix, and the transport argv carries the literal "sudo", so a naive scan
	// reads the one grant this installer must make as the grant it must never
	// make, and the operator learns to ignore the failure.
	forbidden := map[string]bool{TorioProjectsGroup: true, TorioMCPUser: true, dockerGroup: true, "sudo": true, "wheel": true, "root": true}
	for i := 0; i < fr.callCount(); i++ {
		argv := fr.callArgs(i)
		for _, g := range groupArgs(argv) {
			if forbidden[g] {
				t.Errorf("install granted membership of %q: %v", g, argv)
			}
		}
	}
}

// groupArgs returns the group names an identity-mutating argv would grant. Any
// flag whose value is a group list is read; anything else is ignored, so a
// username that merely resembles a group name is not mistaken for one.
func groupArgs(argv []string) []string {
	var out []string
	for i, tok := range argv {
		switch tok {
		case "-G", "-aG", "--groups", "--append-groups":
			if i+1 < len(argv) {
				out = append(out, strings.Split(argv[i+1], ",")...)
			}
		}
	}
	return out
}

func TestInstallMCPBrokerIsIdempotent(t *testing.T) {
	fr := &fakeRunner{script: settledInstallScript()}
	a := New(fr)

	rep, err := a.InstallMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}
	if rep.Changed {
		t.Error("a settled guest reported Changed=true; a re-run must be a no-op")
	}
	for i := 0; i < fr.callCount(); i++ {
		argv := strings.Join(fr.callArgs(i), " ")
		for _, mutator := range []string{"groupadd", "useradd", "usermod", "chmod", "chown"} {
			if strings.Contains(argv, mutator) {
				t.Errorf("settled guest still received a mutation: %q", argv)
			}
		}
	}
}

// TestInstallMCPBrokerDoesNotFailOnLeftoverHermesTokens is a deliberate
// asymmetry with `status`. Credentials sitting under the Hermes profile are
// exactly what the broker exists to end, but refusing to install while they are
// there is a deadlock: the operator cannot build the thing they must migrate to.
// Install proves what install created; the ongoing invariant belongs to status.
func TestInstallMCPBrokerDoesNotFailOnLeftoverHermesTokens(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	a := New(fr)

	rep, err := a.InstallMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}
	for _, c := range rep.Checks {
		if c.Name == "hermes_mcp_tokens" {
			t.Errorf("install ran the ongoing-drift check %q; that belongs to status", c.Name)
		}
	}
}

// TestInstallMCPBrokerReportsRestartRequired: a long-running process does not
// gain a group by having the group database change under it. The always-on
// backend keeps the credentials it started with until it is restarted, so an
// installer that stays silent about this hands the operator a broker their agent
// cannot reach and no reason why.
func TestInstallMCPBrokerReportsRestartRequired(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	a := New(fr)

	rep, err := a.InstallMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}
	if !rep.RestartRequired {
		t.Error("hermes was newly added to the client group but RestartRequired is false")
	}

	fr2 := &fakeRunner{script: settledInstallScript()}
	rep2, err := New(fr2).InstallMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("InstallMCPBroker (settled): unexpected error: %v", err)
	}
	if rep2.RestartRequired {
		t.Error("nothing changed, yet the report demands a backend restart")
	}
}
