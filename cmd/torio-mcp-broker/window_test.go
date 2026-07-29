package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

// withWindowDir points a broker at a window directory and returns it, so a test
// can open a window the way `torio mcp allow-write` does.
func withWindowDir(t *testing.T) (func(*serverConfig), string) {
	t.Helper()
	dir := t.TempDir()
	return func(cfg *serverConfig) { cfg.windowDir = dir }, dir
}

func openWindow(t *testing.T, dir string, until time.Time) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, testService),
		[]byte(mcpbroker.FormatWriteWindow(until)), 0o600); err != nil {
		t.Fatal(err)
	}
}

const writeCall = `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"createJiraIssue","arguments":{"summary":"x"}}}`

// TestWriteToolRefusedWithNoWindow is the guarantee `torio mcp allow-write`
// sells. Without it the command writes a file nothing reads, and every document
// describing the window describes something that does not happen.
func TestWriteToolRefusedWithNoWindow(t *testing.T) {
	tweak, _ := withWindowDir(t)
	up := &fakeUpstream{}
	b := startBroker(t, up, tweak)
	c := b.dial(t)

	c.send(t, writeCall)
	resp := c.response(t)

	if resp.Error == nil {
		t.Fatal("a granted write tool was carried upstream with no window open")
	}
	if !strings.Contains(resp.Error.Message, "write window") {
		t.Errorf("refusal does not name the window: %q", resp.Error.Message)
	}
	// The remedy must be the one that fixes it: opening a window, not editing a
	// policy file the operator has already got right.
	if !strings.Contains(resp.Error.Message, "allow-write") {
		t.Errorf("refusal does not name the remedy: %q", resp.Error.Message)
	}
	if n := len(up.requests); n != 0 {
		t.Errorf("upstream saw %d requests; a refused write must not reach it", n)
	}

	lines := b.auditLines(t)
	if len(lines) != 1 || lines[len(lines)-1].Reason != "write_window_closed" {
		t.Errorf("audit = %+v, want one line reasoned write_window_closed", lines)
	}
}

func TestWriteToolAllowedInsideAWindow(t *testing.T) {
	tweak, dir := withWindowDir(t)
	openWindow(t, dir, time.Now().Add(15*time.Minute))

	up := &fakeUpstream{reply: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"jsonrpc":"2.0","id":9,"result":{"content":[]}}`), nil
	}}
	b := startBroker(t, up, tweak)
	c := b.dial(t)

	c.send(t, writeCall)
	if resp := c.response(t); resp.Error != nil {
		t.Fatalf("a write tool was refused inside an open window: %+v", resp.Error)
	}
	if n := len(up.requests); n != 1 {
		t.Errorf("upstream saw %d requests, want 1", n)
	}
}

func TestExpiredWindowRefusesAgain(t *testing.T) {
	tweak, dir := withWindowDir(t)
	openWindow(t, dir, time.Now().Add(-time.Second))

	up := &fakeUpstream{}
	b := startBroker(t, up, tweak)
	c := b.dial(t)

	c.send(t, writeCall)
	if resp := c.response(t); resp.Error == nil {
		t.Fatal("an expired window still permitted a write tool")
	}
	if n := len(up.requests); n != 0 {
		t.Errorf("upstream saw %d requests; an expired window must refuse", n)
	}
}

// TestReadToolIsUnaffectedByWindows: the window governs write-classified tools
// only. Gating reads on it would make the broker useless outside a window and
// teach the operator to hold one open permanently — the arrangement the window
// exists to replace.
func TestReadToolIsUnaffectedByWindows(t *testing.T) {
	tweak, _ := withWindowDir(t)
	up := &fakeUpstream{reply: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"jsonrpc":"2.0","id":4,"result":{"content":[]}}`), nil
	}}
	b := startBroker(t, up, tweak)
	c := b.dial(t)

	c.send(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"getJiraIssue"}}`)
	if resp := c.response(t); resp.Error != nil {
		t.Fatalf("a read tool was gated on a write window: %+v", resp.Error)
	}
}

// TestUngrantedToolInsideAWindowIsStillUngranted: a window unlocks what policy
// already grants. It is not a second way to grant something.
func TestUngrantedToolInsideAWindowIsStillUngranted(t *testing.T) {
	tweak, dir := withWindowDir(t)
	openWindow(t, dir, time.Now().Add(time.Hour))

	up := &fakeUpstream{}
	b := startBroker(t, up, tweak)
	c := b.dial(t)

	c.send(t, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"deleteJiraIssue"}}`)
	if resp := c.response(t); resp.Error == nil {
		t.Fatal("an ungranted tool was carried because a window was open")
	}
	lines := b.auditLines(t)
	if len(lines) != 1 || lines[0].Reason != "tool_not_granted" {
		t.Errorf("audit = %+v, want tool_not_granted", lines)
	}
}
