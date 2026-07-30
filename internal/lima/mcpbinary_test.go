package lima

import (
	"context"
	"strings"
	"testing"
)

func TestProbeMCPGuestBinaryRejectsAnUntrustedInstallDirectory(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: exitResult(1, "directory hermes:hermes 755\n", "no such file")},
	}}
	rep := &MCPBrokerInstallReport{}
	bin := mcpGuestBinary{target: TorioMCPBrokerPath, digest: "unused"}

	if _, _, err := New(fr).probeMCPGuestBinary(context.Background(), rep, "install:binary", bin); err == nil {
		t.Fatal("an agent-owned /usr/local/bin was accepted as a safe root staging directory")
	}
}

func TestInstallMCPGuestBinaryUsesNoFollowStagingAndSyncsTheDirectory(t *testing.T) {
	const digest = "abc123"
	fr := &fakeRunner{script: []scriptedResponse{
		{result: exitResult(1, "directory root:root 755\n", "no such file")},
		{result: stdoutResult("")},
		{result: stdoutResult("")},
		{result: stdoutResult("")},
		{result: stdoutResult("")},
		{result: stdoutResult("")},
		{result: stdoutResult("directory root:root 755\nregular file root:root 755\n")},
		{result: stdoutResult(digest + "  " + TorioMCPBrokerPath + "\n")},
	}}
	bin := mcpGuestBinary{target: TorioMCPBrokerPath, body: []byte("broker"), digest: digest}

	if _, err := New(fr).ensureMCPGuestBinary(context.Background(), &MCPBrokerInstallReport{}, bin); err != nil {
		t.Fatalf("ensureMCPGuestBinary: %v", err)
	}
	all := ""
	for i := 0; i < fr.callCount(); i++ {
		all += strings.Join(fr.callArgs(i), " ") + "\n"
	}
	if !strings.Contains(all, "oflag=excl,nofollow") {
		t.Fatalf("staging write can follow or replace an existing path:\n%s", all)
	}
	if !strings.Contains(all, "sync -f /usr/local/bin") {
		t.Fatalf("destination directory is not synced after rename:\n%s", all)
	}
}
