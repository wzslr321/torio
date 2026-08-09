package mcpbroker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wzslr321/torio/internal/lima"
)

type fakeUpstream struct {
	tools  []*mcp.Tool
	mu     sync.Mutex
	calls  []string
	result *mcp.CallToolResult
}

func (f *fakeUpstream) ListTools(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	return &mcp.ListToolsResult{Tools: f.tools}, nil
}

func (f *fakeUpstream) CallTool(_ context.Context, p *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, p.Name)
	return f.result, nil
}

func (f *fakeUpstream) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type memoryRecorder struct {
	mu      sync.Mutex
	records []AuditRecord
	err     error
}

func (r *memoryRecorder) Record(rec AuditRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.records = append(r.records, rec)
	return nil
}

func (r *memoryRecorder) all() []AuditRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]AuditRecord(nil), r.records...)
}

func policySet(t *testing.T, tools string) lima.Set {
	t.Helper()
	doc := `{
  "schema_version":"1",
  "service":"tickets",
  "upstream_endpoint":"https://mcp.example.test/mcp",
  "tools":` + tools + `
}`
	set, err := lima.ParseDocuments(map[string][]byte{"tickets.json": []byte(doc)})
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	return set
}

func TestServiceServerExposesAndCarriesOnlyPolicyTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	upstream := &fakeUpstream{
		tools: []*mcp.Tool{
			{Name: "read_ticket", InputSchema: map[string]any{"type": "object"}},
			{Name: "delete_ticket", InputSchema: map[string]any{"type": "object"}},
		},
		result: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "upstream result"}}},
	}
	audit := &memoryRecorder{}
	server, err := NewServiceServer(ctx, ServiceConfig{
		Service:  "tickets",
		Policy:   policySet(t, `[{"name":"read_ticket","writes":false}]`),
		Upstream: upstream,
		Audit:    audit,
		PeerUID:  1001,
	})
	if err != nil {
		t.Fatalf("NewServiceServer: %v", err)
	}

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "read_ticket" {
		t.Fatalf("listed tools = %#v, want only read_ticket", listed.Tools)
	}

	got, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read_ticket",
		Arguments: json.RawMessage(`{"id":"T-1"}`),
	})
	if err != nil {
		t.Fatalf("allowed call: %v", err)
	}
	if len(got.Content) != 1 || got.Content[0].(*mcp.TextContent).Text != "upstream result" {
		t.Fatalf("allowed result = %#v", got)
	}
	if calls := upstream.called(); len(calls) != 1 || calls[0] != "read_ticket" {
		t.Fatalf("upstream calls = %v, want [read_ticket]", calls)
	}

	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "delete_ticket"}); err == nil {
		t.Fatal("denied call succeeded")
	}
	if calls := upstream.called(); len(calls) != 1 {
		t.Fatalf("denied call reached upstream: %v", calls)
	}

	records := audit.all()
	if len(records) != 2 {
		t.Fatalf("audit records = %#v, want allowed and denied", records)
	}
	if records[0].Service != "tickets" || records[0].Tool != "read_ticket" || !records[0].Allowed || records[0].Writes || records[0].PeerUID != 1001 {
		t.Fatalf("allowed audit = %#v", records[0])
	}
	if records[1].Tool != "delete_ticket" || records[1].Allowed {
		t.Fatalf("denied audit = %#v", records[1])
	}
}

func TestServiceServerRefusesPolicyToolMissingUpstream(t *testing.T) {
	_, err := NewServiceServer(context.Background(), ServiceConfig{
		Service: "tickets",
		Policy:  policySet(t, `[{"name":"missing_tool","writes":true}]`),
		Upstream: &fakeUpstream{tools: []*mcp.Tool{
			{Name: "different_tool", InputSchema: map[string]any{"type": "object"}},
		}},
		Audit:   &memoryRecorder{},
		PeerUID: 1001,
	})
	if err == nil || !strings.Contains(err.Error(), "missing_tool") {
		t.Fatalf("NewServiceServer error = %v, want missing policy tool", err)
	}
}

func TestServiceServerRefusesCallWhenAuditCannotBeWritten(t *testing.T) {
	ctx := context.Background()
	upstream := &fakeUpstream{
		tools:  []*mcp.Tool{{Name: "read_ticket", InputSchema: map[string]any{"type": "object"}}},
		result: &mcp.CallToolResult{},
	}
	server, err := NewServiceServer(ctx, ServiceConfig{
		Service:  "tickets",
		Policy:   policySet(t, `[{"name":"read_ticket","writes":false}]`),
		Upstream: upstream,
		Audit:    &memoryRecorder{err: errors.New("disk full")},
		PeerUID:  1001,
	})
	if err != nil {
		t.Fatalf("NewServiceServer: %v", err)
	}

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go server.Run(ctx, serverTransport)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "read_ticket"}); err == nil {
		t.Fatal("call succeeded without an audit record")
	}
	if calls := upstream.called(); len(calls) != 0 {
		t.Fatalf("unaudited call reached upstream: %v", calls)
	}
}

type deadlineUpstream struct{ sawDeadline bool }

func (u *deadlineUpstream) ListTools(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	return &mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: "slow", InputSchema: map[string]any{"type": "object"}}}}, nil
}

func (u *deadlineUpstream) CallTool(ctx context.Context, _ *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	_, u.sawDeadline = ctx.Deadline()
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestServiceServerBoundsEveryUpstreamToolCall(t *testing.T) {
	ctx := context.Background()
	upstream := &deadlineUpstream{}
	server, err := NewServiceServer(ctx, ServiceConfig{
		Service:     "tickets",
		Policy:      policySet(t, `[{"name":"slow","writes":false}]`),
		Upstream:    upstream,
		Audit:       &memoryRecorder{},
		PeerUID:     1001,
		CallTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewServiceServer: %v", err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go server.Run(ctx, serverTransport)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	started := time.Now()
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "slow"}); err == nil {
		t.Fatal("timed-out upstream call succeeded")
	}
	if !upstream.sawDeadline || time.Since(started) > time.Second {
		t.Fatalf("deadline observed = %t, elapsed = %s", upstream.sawDeadline, time.Since(started))
	}
}
