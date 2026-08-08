package lima

import (
	"context"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
)

func TestReconcileMCPClientConfigUsesBackendSpecificRootOfTrust(t *testing.T) {
	grant := PolicyGrant{Services: []PolicyService{{Name: "atlassian", UpstreamEndpoint: "https://api.atlassian.com/v1/mcp", Tools: 1}}}
	cases := []struct {
		name     string
		identity backend.Identity
		wantArg  string
		wantPath string
		calls    int
	}{
		{"hermes agent-owned drift detector", Hermes().Identity(), "/home/hermes/hermes-agent/venv/bin/python", HermesConfigPath, 1},
		{"claude root-owned managed config", backend.Identity{Name: "claude-code", GuestUser: "claude", Home: "/home/claude"}, "/usr/bin/python3", ClaudeManagedMCPPath, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := []scriptedResponse{{result: stdoutResult("changed\n")}}
			if tc.calls == 2 {
				script = append(script, scriptedResponse{result: stdoutResult("unchanged\n")})
			}
			fr := &fakeRunner{script: script}
			rep := &MCPBrokerInstallReport{Instance: InstanceName}
			changed, err := New(fr).reconcileMCPClientConfig(context.Background(), tc.identity, grant, rep)
			if err != nil {
				t.Fatalf("reconcileMCPClientConfig: %v", err)
			}
			if !changed {
				t.Fatal("changed=false, want true")
			}
			args := strings.Join(fr.callArgs(0), " ")
			for _, want := range []string{tc.wantArg, tc.wantPath, TorioMCPRelayPath} {
				if !strings.Contains(args, want) {
					t.Errorf("argv does not contain %q: %q", want, args)
				}
			}
			program := ""
			for i, arg := range fr.callArgs(0) {
				if arg == "-c" && i+1 < len(fr.callArgs(0)) {
					program = fr.callArgs(0)[i+1]
				}
			}
			for _, want := range []string{"os.replace", "os.fsync"} {
				if !strings.Contains(program, want) {
					t.Errorf("atomic config program does not contain %q", want)
				}
			}
			stdin := string(fr.callStdin(0))
			if !strings.Contains(stdin, `"atlassian"`) || strings.Contains(stdin, "Authorization") {
				t.Errorf("unexpected config input: %q", stdin)
			}
			if fr.callCount() != tc.calls {
				t.Fatalf("guest calls = %d, want %d", fr.callCount(), tc.calls)
			}
			if tc.calls == 2 {
				second := strings.Join(fr.callArgs(1), " ")
				if !strings.Contains(second, "sudo -n -u claude -- /usr/bin/python3") || !strings.Contains(second, "/home/claude/.claude.json") {
					t.Errorf("native config cleanup does not run as claude: %q", second)
				}
			}
		})
	}
}

func TestReconcileMCPClientConfigRejectsUnknownBackendWithoutGuestCall(t *testing.T) {
	fr := &fakeRunner{}
	_, err := New(fr).reconcileMCPClientConfig(context.Background(), backend.Identity{Name: "unknown", GuestUser: "agent"}, PolicyGrant{}, &MCPBrokerInstallReport{})
	if err == nil {
		t.Fatal("unknown backend was accepted")
	}
	if fr.callCount() != 0 {
		t.Fatalf("guest calls = %d, want 0", fr.callCount())
	}
}
