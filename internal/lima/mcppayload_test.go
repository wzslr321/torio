package lima

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallMCPPayloadFilesWritesAtomicallyAndVerifiesBytes(t *testing.T) {
	p := testProfile
	dir := t.TempDir()
	files := map[string][]byte{
		p.MCPBrokerArtifact(): []byte("broker payload\n"),
		p.MCPRelayArtifact():  []byte("relay payload\n"),
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Each destination is absent, then receives dd/chown/chmod/mv/fsync and is
	// read back by metadata and digest. The unit follows the two executables.
	responses := make([]scriptedResponse, 0, 27)
	installed := []mcpInstallFile{
		newMCPInstallFile("broker", TorioMCPBrokerPath, "0755", files[p.MCPBrokerArtifact()]),
		newMCPInstallFile("relay", TorioMCPRelayPath, "0755", files[p.MCPRelayArtifact()]),
		newMCPInstallFile("unit", TorioMCPBrokerUnitPath, "0644", mcpBrokerUnit()),
	}
	for _, file := range installed {
		responses = append(responses,
			scriptedResponse{result: exitResult(1, "", "absent")},
			scriptedResponse{result: stdoutResult("")},
			scriptedResponse{result: stdoutResult("")},
			scriptedResponse{result: stdoutResult("")},
			scriptedResponse{result: stdoutResult("")},
			scriptedResponse{result: stdoutResult("")},
			scriptedResponse{result: stdoutResult("root:root " + strings.TrimPrefix(file.mode, "0") + " regular file\n")},
			scriptedResponse{result: stdoutResult(file.sum + "  installed\n")},
		)
	}
	responses = append(responses, scriptedResponse{result: stdoutResult("")}) // daemon-reload
	fr := &fakeRunner{script: responses}
	a := New(fr)
	a.Profile = p
	rep := &MCPBrokerInstallReport{Instance: InstanceName}

	changed, err := a.installMCPPayloadFiles(context.Background(), dir, rep)
	if err != nil {
		t.Fatalf("installMCPPayloadFiles: %v", err)
	}
	if !changed {
		t.Fatal("payload writes occurred but changed=false")
	}

	var stdinBodies int
	for i := 0; i < fr.callCount(); i++ {
		args := strings.Join(fr.callArgs(i), " ")
		if strings.Contains(args, " dd ") {
			stdinBodies++
			if len(fr.callStdin(i)) == 0 {
				t.Errorf("dd call %d received an empty payload", i)
			}
		}
		if strings.Contains(args, " sh -c ") || strings.Contains(args, " bash -c ") {
			t.Errorf("payload install used a shell command string: %q", args)
		}
	}
	if stdinBodies != 3 {
		t.Fatalf("payload body writes = %d, want 3", stdinBodies)
	}
}
