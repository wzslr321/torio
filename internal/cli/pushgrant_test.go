package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/projects"
)

// grantConfig writes the config a granted session needs: the backend that
// declares one, and a pinned key, without which the grant refuses before it
// reaches anything this file is about.
func grantConfig(t *testing.T) string {
	t.Helper()
	cfgHome := t.TempDir()
	dir := filepath.Join(cfgHome, "torio", "instances", "torio-claude-code")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"schema_version":"4","backend":"claude-code","operator_key":"SHA256:pinned","projects":[]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgHome
}

func runGrantCLI(t *testing.T, cfgHome string, service projectService, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	var stdout, stderr bytes.Buffer
	a := &app{
		stdout:             &stdout,
		stderr:             &stderr,
		build:              testBuild(),
		lookupOperatorUser: func() (string, error) { return "testop", nil },
		newProjects: func(*lima.Adapter, lima.BootstrapOptions) projectService {
			return service
		},
	}
	code := runWithApp(context.Background(), a, args)
	return code, stdout.String(), stderr.String()
}

func grantService(access projects.RemoteAccess) *fakeProjectService {
	return &fakeProjectService{
		enterSession: projects.EnterSession{EnterSpec: projects.EnterSpec{
			Project: projects.Project{ID: "demo", Remote: "git@github.com:owner/demo.git", Path: "/home/claude/projects/demo"},
		}},
		remoteAccess: access,
	}
}

// TestPushGrantRefusesARemoteNoAgentCanReach covers the hole the live test found
// twice. A grant against an HTTPS origin hands the session a key that transport
// never consults, and a grant against a host with no key in known_hosts fails at
// verification — after the agent has done the work, with an error that reads
// like a problem with the key the operator just pinned.
func TestPushGrantRefusesARemoteNoAgentCanReach(t *testing.T) {
	for name, tc := range map[string]struct {
		access projects.RemoteAccess
		detail string
	}{
		"https origin": {
			projects.RemoteAccess{Transport: projects.TransportHTTPS},
			"never uses an SSH agent",
		},
		"host key absent": {
			projects.RemoteAccess{Transport: projects.TransportSSH, Host: "github.com"},
			"not in the agent identity's known_hosts",
		},
		"host unreadable": {
			projects.RemoteAccess{Transport: projects.TransportSSH},
			"could not be read as a plain hostname",
		},
	} {
		service := grantService(tc.access)
		code, stdout, stderr := runGrantCLI(t, grantConfig(t), service,
			"--backend", "claude-code", "project", "agent", "demo", "--push-grant")

		if code != int(ExitPrecondition) {
			t.Errorf("%s: exit = %d, want %d", name, code, ExitPrecondition)
		}
		if !strings.Contains(stderr, tc.detail) {
			t.Errorf("%s: stderr = %q, want it to name %q", name, stderr, tc.detail)
		}
		if strings.Contains(stdout, "starting") {
			t.Errorf("%s: a session was announced before the refusal: %q", name, stdout)
		}
	}
}

// TestPushGrantAsksAboutTheAgentIdentity is the distinction that made this
// necessary. The operator and the backend identity have different home
// directories, so a host key one of them trusts says nothing about the other —
// and the grant runs as the backend identity.
func TestPushGrantAsksAboutTheAgentIdentity(t *testing.T) {
	service := grantService(projects.RemoteAccess{Transport: projects.TransportHTTPS})
	runGrantCLI(t, grantConfig(t), service,
		"--backend", "claude-code", "project", "agent", "demo", "--push-grant")

	if service.remoteAccessCalls != 1 {
		t.Fatalf("RemoteAccess called %d times, want once", service.remoteAccessCalls)
	}
	if service.remoteAccessWho != projects.AgentIdentity {
		t.Errorf("RemoteAccess asked about %v, want the agent identity", service.remoteAccessWho)
	}
	if service.remoteAccessID != "demo" {
		t.Errorf("RemoteAccess asked about %q, want the project on the command line", service.remoteAccessID)
	}
}
