package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

const testPolicy = `{"schema_version":"1","service":"tickets","upstream_endpoint":"https://mcp.example.test/mcp","tools":[{"name":"read_ticket","writes":false}]}`

func TestRunDaemonPublishesNothingBeforeOperatorLogin(t *testing.T) {
	root := t.TempDir()
	policyDir := filepath.Join(root, "policy")
	if err := os.Mkdir(policyDir, 0o755); err != nil {
		t.Fatalf("mkdir policy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "tickets.json"), []byte(testPolicy), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	socketDir := filepath.Join(root, "run")
	if err := os.Mkdir(socketDir, 0o750); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}

	err := runDaemon(context.Background(), daemonConfig{
		policyDir: policyDir,
		socketDir: socketDir,
		storeDir:  filepath.Join(root, "oauth"),
		auditPath: filepath.Join(root, "audit.jsonl"),
	})
	if !errors.Is(err, mcpbroker.ErrOAuthSessionNotFound) {
		t.Fatalf("runDaemon error = %v, want login precondition", err)
	}
	entries, readErr := os.ReadDir(socketDir)
	if readErr != nil {
		t.Fatalf("read runtime: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sock") {
			t.Fatalf("socket %s was published before login", entry.Name())
		}
	}
}

func TestLoadPolicyDirRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	policyDir := filepath.Join(root, "policy")
	if err := os.Mkdir(policyDir, 0o755); err != nil {
		t.Fatalf("mkdir policy: %v", err)
	}
	target := filepath.Join(root, "outside.json")
	if err := os.WriteFile(target, []byte(testPolicy), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(policyDir, "tickets.json")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := loadPolicyDir(policyDir); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Fatalf("loadPolicyDir error = %v, want symlink refusal", err)
	}
}
