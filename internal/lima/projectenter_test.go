package lima

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectEnterHelperIsAValidNonPushWorkspaceShell(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available; the guest helper cannot be parsed here")
	}

	content, err := projectHelper(embeddedProjectEnter, HermesWorkspacePath, "project enter")
	if err != nil {
		t.Fatalf("resolving the guest helper: %v", err)
	}
	path := filepath.Join(t.TempDir(), "torio-project-enter")
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("writing the guest helper: %v", err)
	}
	if out, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v: %s", err, out)
	}

	code := string(embeddedProjectEnter)
	if !strings.Contains(code, "No SSH agent is forwarded") {
		t.Fatalf("helper does not distinguish the ordinary session from a push-capable shell")
	}
	for _, forbidden := range []string{"SSH_AUTH_SOCK", "ssh-add", "sudo", "runuser", "su -"} {
		if line := lineContaining(code, forbidden); line != "" {
			t.Errorf("helper line %q uses %q", line, forbidden)
		}
	}
	if !strings.Contains(code, `exec sg "$group" -c 'exec bash --norc -i'`) {
		t.Errorf("helper does not enter the shared project group without privilege")
	}
}
