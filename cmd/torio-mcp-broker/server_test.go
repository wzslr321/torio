package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

const (
	// testService is the service every fixture in this file speaks for.
	testService = "atlassian"
	// testUID stands in for the uid the kernel reports over SO_PEERCRED. It is a
	// plausible non-root uid so that a record carrying 0 is visibly wrong.
	testUID = 1001
)

// policyDocument grants two of the three tools the fake upstream publishes. The
// third, deleteJiraIssue, is the tool every enforcement test asks for: it exists
// upstream, it is not granted, and nothing about it may reach the client.
const policyDocument = `{
  "schema_version": "1",
  "service": "atlassian",
  "upstream_endpoint": "https://mcp.example.invalid/v1",
  "tools": [
    {"name": "getJiraIssue", "writes": false},
    {"name": "createJiraIssue", "writes": true}
  ]
}`

// syncBuffer collects what the broker writes while it is still running. A plain
// bytes.Buffer would be a data race under -race, and the races that matter here
// are the ones the audit log would hide.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeUpstream stands in for the MCP server on the other side of the broker. It
// records what it was handed, so a test can assert both that a denied call never
// reached it and that an allowed one reached it byte for byte.
type fakeUpstream struct {
	mu       sync.Mutex
	requests []string
	reply    func(context.Context, json.RawMessage) (json.RawMessage, error)
}

func (f *fakeUpstream) roundTrip(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	f.requests = append(f.requests, string(request))
	f.mu.Unlock()
	if f.reply == nil {
		return json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`), nil
	}
	return f.reply(ctx, request)
}

func (f *fakeUpstream) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// shortTempDir is a socket base short enough to survive sun_path, the kernel's
// ~104-byte limit on a unix socket address. t.TempDir() embeds the test name and
// on darwin overruns it, which surfaces as EINVAL from bind and reads like a bug
// in the broker rather than in the fixture.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tmb")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// loadPolicy writes one policy document and loads it through the real policy
// engine. Tests decide against mcpbroker.Set, never against a hand-built stand-in:
// a fixture that granted differently from the loader would prove nothing.
func loadPolicy(t *testing.T, service, doc string) (mcpbroker.Set, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, service+".json")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	set, err := mcpbroker.Load(dir)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	return set, path
}

// testBroker is one running broker with its two out-of-band channels captured.
type testBroker struct {
	socket     string
	policyPath string
	audit      *syncBuffer
	stderr     *syncBuffer
}

// startBroker runs a broker over a real unix socket. Real sockets rather than an
// in-memory pipe: the transport is part of what is under test, and the peer
// credential the audit line carries has nowhere else to come from.
func startBroker(t *testing.T, up upstream, tweak func(*serverConfig)) *testBroker {
	t.Helper()
	set, policyPath := loadPolicy(t, testService, policyDocument)
	audit, stderr := &syncBuffer{}, &syncBuffer{}

	cfg := serverConfig{
		service:     testService,
		policy:      set,
		policyPath:  policyPath,
		upstream:    up,
		audit:       newAuditSink(audit),
		peerUID:     func(*net.UnixConn) (uint32, error) { return testUID, nil },
		log:         slog.New(slog.NewTextHandler(stderr, nil)),
		callTimeout: 5 * time.Second,
	}
	if tweak != nil {
		tweak(&cfg)
	}
	srv, err := newServer(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ln, err := listenService(shortTempDir(t), cfg.service, -1)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.serve(ctx, ln); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("serve did not return after its context was cancelled")
		}
	})
	return &testBroker{socket: ln.Addr().String(), policyPath: cfg.policyPath, audit: audit, stderr: stderr}
}

// client is one MCP client on the broker's socket, framed the way the relay
// frames it: one JSON object per line.
type client struct {
	conn *net.UnixConn
	r    *bufio.Reader
}

func (b *testBroker) dial(t *testing.T) *client {
	t.Helper()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: b.socket, Net: "unix"})
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	// A deadline rather than a hang: a broker that stops answering must fail the
	// test in seconds, not park the suite.
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	return &client{conn: conn, r: bufio.NewReader(conn)}
}

func (c *client) send(t *testing.T, line string) {
	t.Helper()
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func (c *client) receive(t *testing.T) string {
	t.Helper()
	line, err := c.r.ReadString('\n')
	if err != nil {
		t.Fatalf("receive: %v (got %q)", err, line)
	}
	return line
}

// rpcResponse is the response envelope a test asserts on. It is deliberately
// separate from the production types: a test that reused them would follow them
// into whatever shape they drifted to.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *client) response(t *testing.T) rpcResponse {
	t.Helper()
	line := c.receive(t)
	var resp rpcResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("response %q is not JSON: %v", line, err)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("response jsonrpc = %q, want \"2.0\" (line %q)", resp.JSONRPC, line)
	}
	return resp
}

// upstreamToolsList is what the service publishes: the two granted tools and one
// that is not granted. The entries carry the fields a real MCP server sends, so
// the filter is shown to preserve an entry rather than rebuild it.
const upstreamToolsList = `{"jsonrpc":"2.0","id":9,"result":{"tools":[
  {"name":"getJiraIssue","description":"read an issue","inputSchema":{"type":"object","properties":{"issueKey":{"type":"string"}}}},
  {"name":"deleteJiraIssue","description":"delete an issue","inputSchema":{"type":"object"},"annotations":{"destructiveHint":true}},
  {"name":"createJiraIssue","description":"create an issue","inputSchema":{"type":"object"}}
],"nextCursor":"page-2"}}`

// The visible surface must equal the granted surface. An agent that can see a
// tool it may not call will call it, and every one of those attempts is a denial
// line that means nothing — the log fills with refusals of a tool nobody
// deliberately asked for, and the refusals that matter get lost among them.
//
// A granted entry passes through whole. Filtering is choosing which entries the
// client sees, not rewriting the ones it does: an entry with a rebuilt schema
// would be a tool description Torio wrote, and the client would call the tool
// according to it.
func TestToolsListIsFilteredToTheGrantedSurface(t *testing.T) {
	up := &fakeUpstream{reply: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(upstreamToolsList), nil
	}}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	c.send(t, `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`)
	line := c.receive(t)

	if strings.Contains(line, "deleteJiraIssue") {
		t.Errorf("response mentions an ungranted tool: %s", line)
	}
	var resp struct {
		ID     json.RawMessage `json:"id"`
		Result struct {
			Tools      []json.RawMessage `json:"tools"`
			NextCursor string            `json:"nextCursor"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("response %q is not JSON: %v", line, err)
	}
	if string(resp.ID) != "9" {
		t.Errorf("response id = %s, want 9", resp.ID)
	}
	if resp.Result.NextCursor != "page-2" {
		t.Errorf("nextCursor = %q, want it preserved: filtering a page must not lose the rest of them", resp.Result.NextCursor)
	}

	var names []string
	for _, entry := range resp.Result.Tools {
		var tool struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		}
		if err := json.Unmarshal(entry, &tool); err != nil {
			t.Fatalf("tool entry %s is not JSON: %v", entry, err)
		}
		names = append(names, tool.Name)
		if tool.Description == "" || len(tool.InputSchema) == 0 {
			t.Errorf("tool entry %s lost fields the client needs to call it", entry)
		}
	}
	want := []string{"getJiraIssue", "createJiraIssue"}
	if !slices.Equal(names, want) {
		t.Errorf("visible tools = %v, want exactly the granted ones %v", names, want)
	}
}

// A listing the broker cannot filter is withheld. The tempting failure is to
// pass it through — the client asked, the upstream answered — but a listing the
// broker did not filter is a listing that can name tools nobody granted, and the
// whole guarantee is that the visible surface equals the granted one.
func TestUnfilterableToolsListIsWithheld(t *testing.T) {
	cases := map[string]string{
		"tools is not an array":  `{"jsonrpc":"2.0","id":1,"result":{"tools":{"deleteJiraIssue":{}}}}`,
		"result has no tools":    `{"jsonrpc":"2.0","id":1,"result":{"nextCursor":"page-2"}}`,
		"entry has no name":      `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"description":"deleteJiraIssue"}]}}`,
		"reply is not a message": `{"tools":["deleteJiraIssue"]}`,
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			up := &fakeUpstream{reply: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(reply), nil
			}}
			b := startBroker(t, up, nil)
			c := b.dial(t)

			c.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
			line := c.receive(t)

			if strings.Contains(line, "deleteJiraIssue") {
				t.Errorf("response leaked upstream listing content: %s", line)
			}
			var resp rpcResponse
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				t.Fatalf("response %q is not JSON: %v", line, err)
			}
			if resp.Error == nil || resp.Error.Code != codeInternalError {
				t.Errorf("response = %s, want a JSON-RPC internal error", line)
			}
			// The broker's own log must name the failure without quoting the reply
			// that caused it: an upstream listing is content like any other.
			if diag := b.stderr.String(); strings.Contains(diag, "deleteJiraIssue") {
				t.Errorf("stderr quoted the upstream reply: %q", diag)
			}
		})
	}
}

// An upstream error reaches the client unchanged. There is no listing in it to
// filter, and rewriting it would replace the upstream's own account of what went
// wrong with Torio's guess at it.
func TestToolsListPassesUpstreamErrorThrough(t *testing.T) {
	const reply = `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"upstream is rebuilding its index"}}`
	up := &fakeUpstream{reply: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(reply), nil
	}}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	if got := strings.TrimSuffix(c.receive(t), "\n"); got != reply {
		t.Errorf("client got %s, want the upstream error unchanged %s", got, reply)
	}
}

// auditLine is the rendered audit record, read back the way an operator's tools
// would read it.
type auditLine struct {
	Timestamp string `json:"ts"`
	Service   string `json:"service"`
	Tool      string `json:"tool"`
	UID       uint32 `json:"uid"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
}

func (b *testBroker) auditLines(t *testing.T) []auditLine {
	t.Helper()
	var lines []auditLine
	for _, raw := range strings.Split(strings.TrimSpace(b.audit.String()), "\n") {
		if raw == "" {
			continue
		}
		var line auditLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("audit line %q is not JSON: %v", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

// Both verdicts are recorded, and recorded the same way. A log that only holds
// denials makes an allowed write to Jira invisible, which is the opposite of what
// ADR-0022 is for: the point is that what the broker did is legible afterwards,
// not that its refusals are.
//
// The uid comes from the kernel, so the record attributes the call to an
// identity nobody presented.
func TestEveryDecisionIsAudited(t *testing.T) {
	up := &fakeUpstream{reply: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`), nil
	}}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"getJiraIssue","arguments":{"issueKey":"TOR-1"}}}`)
	c.response(t)
	c.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deleteJiraIssue","arguments":{"issueKey":"TOR-1"}}}`)
	c.response(t)

	lines := b.auditLines(t)
	if len(lines) != 2 {
		t.Fatalf("audit holds %d lines, want 2 (one per decision): %q", len(lines), b.audit.String())
	}
	want := []auditLine{
		{Service: testService, Tool: "getJiraIssue", UID: testUID, Decision: "allow", Reason: "granted"},
		{Service: testService, Tool: "deleteJiraIssue", UID: testUID, Decision: "deny", Reason: "tool_not_granted"},
	}
	for i, w := range want {
		got := lines[i]
		if got.Timestamp == "" {
			t.Errorf("line %d has no timestamp", i)
		}
		got.Timestamp = ""
		if got != w {
			t.Errorf("audit line %d = %+v, want %+v", i, got, w)
		}
	}
}

// A message the broker will not hold is refused with its limit named, and the
// connection ends there. Framing is what forces the close: an oversized line was
// read in pieces, so there is no way to know where the next message begins, and a
// broker that kept reading would eventually interpret the tail of one message as
// the whole of another.
func TestOversizedRequestIsRefusedAndEndsTheConnection(t *testing.T) {
	up := &fakeUpstream{}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	// The write happens in the background: the broker stops reading as soon as the
	// bound is hit, so the client is left holding bytes nobody will take.
	go func() {
		flood := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"getJiraIssue","arguments":{"body":"` +
			strings.Repeat("A", maxRequestBytes) + `"}}}`
		c.conn.Write([]byte(flood + "\n"))
	}()

	resp := c.response(t)
	if resp.Error == nil {
		t.Fatalf("got result %s, want a refusal", resp.Result)
	}
	if resp.Error.Code != codeInvalidRequest {
		t.Errorf("error code = %d, want %d", resp.Error.Code, codeInvalidRequest)
	}
	if !strings.Contains(resp.Error.Message, strconv.Itoa(maxRequestBytes)) {
		t.Errorf("error message = %q, want the limit named", resp.Error.Message)
	}
	if _, err := c.r.ReadString('\n'); err == nil {
		t.Error("connection still carries messages after an oversized one; framing was lost and cannot be resynchronised")
	}
	if seen := up.seen(); len(seen) != 0 {
		t.Errorf("upstream saw %d requests, want 0", len(seen))
	}
}

// A connection whose caller the kernel will not name is refused before a byte is
// served. The uid is what makes an audit line evidence rather than a note, so a
// call that cannot be attributed is a call that must not happen.
func TestConnectionWithoutPeerCredentialsIsRefused(t *testing.T) {
	up := &fakeUpstream{}
	b := startBroker(t, up, func(cfg *serverConfig) {
		cfg.peerUID = func(*net.UnixConn) (uint32, error) {
			return 0, errors.New("SO_PEERCRED unavailable")
		}
	})
	c := b.dial(t)

	if line, err := c.r.ReadString('\n'); err == nil {
		t.Errorf("broker answered %q, want the connection closed unserved", line)
	}
	if seen := up.seen(); len(seen) != 0 {
		t.Errorf("upstream saw %d requests, want 0", len(seen))
	}
	if audit := b.audit.String(); audit != "" {
		t.Errorf("audit = %q, want empty", audit)
	}
	if diag := b.stderr.String(); !strings.Contains(diag, "peer credentials") {
		t.Errorf("stderr = %q, want the refusal explained", diag)
	}
}

// failingWriter is an audit log that cannot be written to: a full disk, a
// read-only mount, a rotated file nobody reopened.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("audit sink is unwritable") }

// A decision that cannot be recorded is not acted on. The tempting failure is to
// serve the call and log the logging failure — the policy did say yes — but then
// the broker has carried a call it cannot account for, and being able to account
// for calls is the reason it exists (ADR-0022 §5).
func TestUnrecordableDecisionRefusesTheCall(t *testing.T) {
	up := &fakeUpstream{}
	b := startBroker(t, up, func(cfg *serverConfig) {
		cfg.audit = newAuditSink(failingWriter{})
	})
	c := b.dial(t)

	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"getJiraIssue","arguments":{}}}`)

	resp := c.response(t)
	if resp.Error == nil {
		t.Fatalf("got result %s, want a refusal", resp.Result)
	}
	if resp.Error.Code != codeInternalError {
		t.Errorf("error code = %d, want %d", resp.Error.Code, codeInternalError)
	}
	if seen := up.seen(); len(seen) != 0 {
		t.Errorf("upstream saw %d requests, want 0: an unrecorded allow must not be carried", len(seen))
	}
}

// The handshake has to get through. initialize, notifications/initialized and
// ping are how an MCP session starts and stays alive; a broker that enforced the
// tool rules perfectly and dropped the handshake would be a broker no client ever
// finishes connecting to.
//
// A notification gets no reply, ever. JSON-RPC forbids one, and a stray message
// on the stream would be read as the answer to whatever the client asks next.
func TestHandshakeIsCarriedAndNotificationsGetNoReply(t *testing.T) {
	up := &fakeUpstream{reply: func(_ context.Context, req json.RawMessage) (json.RawMessage, error) {
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(req, &envelope); err != nil {
			return nil, err
		}
		if len(envelope.ID) == 0 {
			// A real MCP server answers a notification with nothing. This one answers
			// with something, so the broker is shown to drop it rather than to have
			// had nothing to drop.
			return json.RawMessage(`{"jsonrpc":"2.0","id":99,"result":{"unsolicited":true}}`), nil
		}
		return json.RawMessage(`{"jsonrpc":"2.0","id":` + string(envelope.ID) + `,"result":{}}`), nil
	}}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`)
	if resp := c.response(t); string(resp.ID) != "1" {
		t.Errorf("initialize answered with id %s, want 1", resp.ID)
	}

	c.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	c.send(t, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if resp := c.response(t); string(resp.ID) != "2" {
		t.Errorf("next response has id %s, want 2: the notification was answered", resp.ID)
	}

	if seen := up.seen(); len(seen) != 3 {
		t.Errorf("upstream saw %d messages, want 3: the handshake is carried, not answered locally", len(seen))
	}
	if audit := b.audit.String(); audit != "" {
		t.Errorf("audit = %q, want empty: the handshake is not a policy decision", audit)
	}
}

// A method that must be a request is refused when it arrives without an id.
// Guessing that a client meant a request would mean carrying a tools/call whose
// result has nowhere to go; guessing it meant a notification would mean carrying
// one silently. Neither is a guess worth making.
func TestRequestMethodWithoutAnIDIsRefused(t *testing.T) {
	up := &fakeUpstream{}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	c.send(t, `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"getJiraIssue","arguments":{}}}`)

	resp := c.response(t)
	if resp.Error == nil || resp.Error.Code != codeInvalidRequest {
		t.Fatalf("response = %+v, want an invalid request error", resp)
	}
	if seen := up.seen(); len(seen) != 0 {
		t.Errorf("upstream saw %d requests, want 0", len(seen))
	}
	if audit := b.audit.String(); audit != "" {
		t.Errorf("audit = %q, want empty: nothing was decided", audit)
	}
}

// One client must not be able to stall another. An MCP tool call can take a long
// time upstream — a Jira search, a Confluence render — and there is one broker
// for every client on the guest, so a broker that served one connection at a time
// would turn any slow call into an outage for everybody else.
func TestASlowCallDoesNotStallAnotherClient(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	up := &fakeUpstream{reply: func(ctx context.Context, req json.RawMessage) (json.RawMessage, error) {
		if calls.Add(1) == 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`), nil
	}}
	b := startBroker(t, up, nil)

	const call = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"getJiraIssue","arguments":{}}}`
	slow := b.dial(t)
	slow.send(t, call)
	// The slow client's request has to be in the upstream before the second client
	// arrives, or the test would pass on a broker that simply served them in order.
	waitFor(t, func() bool { return calls.Load() == 1 })

	quick := b.dial(t)
	quick.send(t, call)
	if resp := quick.response(t); resp.Error != nil {
		t.Fatalf("second client got an error while the first was blocked: %+v", resp.Error)
	}

	close(release)
	if resp := slow.response(t); resp.Error != nil {
		t.Errorf("first client got an error after its call was released: %+v", resp.Error)
	}
	if lines := b.auditLines(t); len(lines) != 2 {
		t.Errorf("audit holds %d lines, want 2: concurrent decisions are recorded once each", len(lines))
	}
}

// A broker that stops accepting must let go of the connections it is already
// serving. An MCP session is idle almost all of the time and ends when the client
// says so, so a serve loop that waited for its clients before returning would
// hang exactly when something has gone wrong with the socket — and the daemon
// around it would hang with it, holding the sockets of every other service.
func TestServeReleasesItsConnectionsWhenAcceptFails(t *testing.T) {
	set, policyPath := loadPolicy(t, testService, policyDocument)
	stderr := &syncBuffer{}
	srv, err := newServer(serverConfig{
		service:     testService,
		policy:      set,
		policyPath:  policyPath,
		upstream:    &fakeUpstream{},
		audit:       newAuditSink(&syncBuffer{}),
		peerUID:     func(*net.UnixConn) (uint32, error) { return testUID, nil },
		log:         slog.New(slog.NewTextHandler(stderr, nil)),
		callTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ln, err := listenService(shortTempDir(t), testService, -1)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	// The context stays alive: this is the socket failing, not a shutdown.
	done := make(chan error, 1)
	go func() { done <- srv.serve(context.Background(), ln) }()

	b := &testBroker{socket: ln.Addr().String()}
	c := b.dial(t)
	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	c.response(t)

	ln.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Error("serve returned no error after its listener failed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return while a client was still connected")
	}
}

// waitFor polls until cond holds, so a test asserts on a state rather than on a
// sleep that is either flaky or slow.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never held")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Nothing a call carries may be written down. Assume the arguments are a
// Confluence page and the reply is a Jira issue: an audit line or a log line that
// held either would be a second, durable copy of content the broker exists to
// mediate, readable by whoever reads logs and outliving the call (ADR-0022 §5).
//
// Both verdicts are exercised, because the denial is the tempting one — nothing
// went upstream, so it feels safe to record what was attempted.
func TestNoCallContentReachesTheAuditOrTheLog(t *testing.T) {
	const argumentCanary = "CANARY-ARGUMENT-8f3a"
	const replyCanary = "CANARY-REPLY-2b71"

	up := &fakeUpstream{reply: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"` + replyCanary + `"}]}}`), nil
	}}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	for i, tool := range []string{"getJiraIssue", "deleteJiraIssue"} {
		c.send(t, `{"jsonrpc":"2.0","id":`+strconv.Itoa(i+1)+`,"method":"tools/call","params":{"name":"`+tool+
			`","arguments":{"body":"`+argumentCanary+`"}}}`)
		c.response(t)
	}
	// A malformed message is the other way content arrives: the parser's complaint
	// is about bytes the caller chose, so it must not be quoted either.
	c.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":`+argumentCanary+`}`)
	c.response(t)

	if lines := b.auditLines(t); len(lines) != 2 {
		t.Fatalf("audit holds %d lines, want 2", len(lines))
	}
	for _, where := range []struct {
		name string
		text string
	}{{"audit", b.audit.String()}, {"stderr", b.stderr.String()}} {
		if strings.Contains(where.text, argumentCanary) {
			t.Errorf("%s holds tool call arguments: %q", where.name, where.text)
		}
		if strings.Contains(where.text, replyCanary) {
			t.Errorf("%s holds an upstream reply: %q", where.name, where.text)
		}
	}
}

// A denied tool name is the one caller-chosen string the broker does write down,
// which makes the audit log a narrow write channel into a privileged file
// (ADR-0022 §5). It stays narrow: the name is bounded and escaped, so a caller
// cannot choose how much the broker logs, and cannot end the line and append one
// of its own claiming a call was allowed.
func TestDeniedToolNameCannotForgeAnAuditLine(t *testing.T) {
	forged := strings.Repeat("A", 400) + "\",\"decision\":\"allow"
	b := startBroker(t, &fakeUpstream{}, nil)
	c := b.dial(t)

	name, err := json.Marshal(forged)
	if err != nil {
		t.Fatalf("encode tool name: %v", err)
	}
	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":`+string(name)+`}}`)
	c.response(t)

	lines := b.auditLines(t)
	if len(lines) != 1 {
		t.Fatalf("audit holds %d lines, want 1: a tool name must not be able to add one", len(lines))
	}
	if lines[0].Decision != "deny" {
		t.Errorf("decision = %q, want deny", lines[0].Decision)
	}
	if len(lines[0].Tool) > 200 {
		t.Errorf("audited tool name is %d bytes; the caller chose how much the broker writes", len(lines[0].Tool))
	}
}

// A granted call is carried, not interpreted. The broker forwards the line it
// received, byte for byte, and returns the reply the same way: anything else
// would mean re-encoding a message whose contents are Jira and Confluence
// material, and a broker that rewrites a request is a broker that can silently
// change what a tool was asked to do.
func TestGrantedToolCallIsCarriedVerbatim(t *testing.T) {
	const request = `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"getJiraIssue","arguments":{"issueKey":"TOR-1"}}}`
	const reply = `{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"issue body"}],"isError":false,"_meta":{"upstream":"kept"}}}`

	up := &fakeUpstream{reply: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(reply), nil
	}}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	c.send(t, request)

	if got := strings.TrimSuffix(c.receive(t), "\n"); got != reply {
		t.Errorf("client got %s, want the upstream reply unchanged %s", got, reply)
	}
	seen := up.seen()
	if len(seen) != 1 {
		t.Fatalf("upstream saw %d requests, want 1", len(seen))
	}
	if seen[0] != request {
		t.Errorf("upstream got %s, want the request unchanged %s", seen[0], request)
	}
}

// The enforcement point that matters most: a tool the policy does not list is
// refused, and the refusal is actionable. "Denied" alone tells the operator
// nothing they can act on — the remedy is a named, root-owned file, so the
// message names it.
//
// The upstream must not be reached at all. A denial that still made the call and
// discarded the answer would leave the same trace at Atlassian as an allowed one.
func TestUngrantedToolCallIsDenied(t *testing.T) {
	up := &fakeUpstream{}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	c.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"deleteJiraIssue","arguments":{"issueKey":"TOR-1"}}}`)

	resp := c.response(t)
	if resp.Error == nil {
		t.Fatalf("got result %s, want a denial", resp.Result)
	}
	if resp.Error.Code != codeDenied {
		t.Errorf("error code = %d, want %d", resp.Error.Code, codeDenied)
	}
	if string(resp.ID) != "3" {
		t.Errorf("error id = %s, want 3", resp.ID)
	}
	msg := resp.Error.Message
	if !strings.Contains(msg, b.policyPath) {
		t.Errorf("error message = %q, want it to name the policy document %q", msg, b.policyPath)
	}
	if !strings.Contains(msg, "deleteJiraIssue") {
		t.Errorf("error message = %q, want it to name the tool that was refused", msg)
	}
	if seen := up.seen(); len(seen) != 0 {
		t.Errorf("upstream saw %d requests, want 0: a denied call never happens", len(seen))
	}
}

// The policy document's vocabulary is tools: a grant lists tool names and
// nothing else. So every other MCP surface — resources, prompts, sampling — is
// refused rather than carried, because there is no document that could say
// whether it is allowed, and a surface the policy cannot describe is a surface
// the broker cannot enforce. The refusal has to say that, otherwise the operator
// reads "method not found" and goes looking for a typo.
//
// A refusal also ends one message, not the session: the connection must still
// carry the next request.
func TestUngovernedMethodIsRefused(t *testing.T) {
	up := &fakeUpstream{}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	c.send(t, `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"file:///etc/shadow"}}`)

	resp := c.response(t)
	if resp.Error == nil {
		t.Fatalf("got result %s, want a JSON-RPC error", resp.Result)
	}
	if resp.Error.Code != codeMethodNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, codeMethodNotFound)
	}
	if string(resp.ID) != "7" {
		t.Errorf("error id = %s, want 7", resp.ID)
	}
	if msg := resp.Error.Message; !strings.Contains(msg, "tools/call") || !strings.Contains(msg, "tools/list") {
		t.Errorf("error message = %q, want it to name the surface the broker does carry", msg)
	}
	if seen := up.seen(); len(seen) != 0 {
		t.Errorf("upstream saw %d requests, want 0: an ungoverned method is not forwarded", len(seen))
	}

	// The session survives the refusal.
	c.send(t, `{"jsonrpc":"2.0","id":8,"method":"resources/read"}`)
	if next := c.response(t); string(next.ID) != "8" {
		t.Errorf("second response id = %s, want 8: one refusal must not end the session", next.ID)
	}
}

// A line the broker cannot parse is refused, not guessed at. The refusal is a
// JSON-RPC error with a null id — the id is exactly what could not be read — and
// nothing reaches the upstream, because a request nobody could parse is a request
// nobody decided about.
func TestMalformedRequestIsRefused(t *testing.T) {
	up := &fakeUpstream{}
	b := startBroker(t, up, nil)
	c := b.dial(t)

	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/li`)

	resp := c.response(t)
	if resp.Error == nil {
		t.Fatalf("got result %s, want a JSON-RPC error", resp.Result)
	}
	if resp.Error.Code != codeParseError {
		t.Errorf("error code = %d, want %d", resp.Error.Code, codeParseError)
	}
	if string(resp.ID) != "null" {
		t.Errorf("error id = %s, want null: the id of an unparsable request is unknown", resp.ID)
	}
	if seen := up.seen(); len(seen) != 0 {
		t.Errorf("upstream saw %d requests, want 0", len(seen))
	}
	if audit := b.audit.String(); audit != "" {
		t.Errorf("audit = %q, want empty: no policy decision was taken", audit)
	}
	// The diagnostic must name the service and nothing else. Echoing the rejected
	// bytes would put caller-chosen content into a privileged log.
	if diag := b.stderr.String(); !strings.Contains(diag, testService) {
		t.Errorf("stderr = %q, want the service named", diag)
	}
}
