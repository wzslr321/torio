package main

import (
	"context"
	"encoding/json"
	"testing"
)

const writeCall = `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"createJiraIssue","arguments":{"summary":"x"}}}`

func TestGrantedWriteToolDoesNotDependOnProposedWriteWindow(t *testing.T) {
	up := &fakeUpstream{reply: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"jsonrpc":"2.0","id":9,"result":{"content":[]}}`), nil
	}}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	c.send(t, writeCall)
	if resp := c.response(t); resp.Error != nil {
		t.Fatalf("a policy-granted write tool was refused by Proposed ADR-0004 behavior: %+v", resp.Error)
	}
	if n := len(up.requests); n != 1 {
		t.Errorf("upstream saw %d requests, want 1", n)
	}
}
