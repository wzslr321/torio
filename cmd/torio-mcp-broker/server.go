package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

// maxRequestBytes bounds one client message.
//
// The bound is what keeps a client from choosing how much memory the broker
// spends: without it, one line with no newline in it is an unbounded allocation.
// A megabyte is far above any real MCP request — the largest by a wide margin is
// a tool call carrying a document body — and a message over the bound is refused
// with the limit named, never silently truncated into a different request.
const maxRequestBytes = 1 << 20

// readBufferBytes is the per-connection read buffer. It grows to
// maxRequestBytes only for a client that actually sends a message that large.
const readBufferBytes = 64 << 10

// defaultUpstreamTimeout bounds one upstream call. Every call the broker makes
// carries a deadline (AGENTS §6): without one, an upstream that accepts a
// connection and never answers holds a client's connection open forever, and the
// client cannot tell that from a slow tool.
const defaultUpstreamTimeout = 60 * time.Second

// writeTimeout bounds how long the broker will wait to hand a response to a
// client that has stopped reading. It bounds one connection's goroutine; other
// connections are unaffected either way, since each has its own.
const writeTimeout = 30 * time.Second

// auditSink serialises audit records onto one writer.
//
// Every connection is served by its own goroutine and they all record to the
// same place, so the lock is not incidental: two interleaved writes would
// produce a line that parses as neither decision, and an audit log that can be
// scrambled by concurrency is not evidence.
type auditSink struct {
	mu sync.Mutex
	w  io.Writer
}

func newAuditSink(w io.Writer) *auditSink { return &auditSink{w: w} }

func (a *auditSink) record(rec mcpbroker.AuditRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return mcpbroker.WriteAudit(a.w, rec)
}

// serverConfig is everything one service's broker needs. The fields that reach
// outside the process — the upstream, the audit sink, the peer credential source
// — are injected rather than reached for, which is what lets every rule in this
// file be exercised without root and without a network.
type serverConfig struct {
	// service is the service this broker speaks for. One socket, one service.
	service string
	// policy is the loaded policy set. Decisions are taken against it and never
	// against a copy of it kept somewhere more convenient.
	policy mcpbroker.Set
	// policyPath is the document the grant came from. It is carried so a denial
	// can name the file an operator has to edit; a denial that says only "denied"
	// costs somebody an afternoon.
	policyPath string
	upstream   upstream
	audit      *auditSink
	// peerUID reports the uid the kernel attributes to a connection.
	peerUID func(*net.UnixConn) (uint32, error)
	// log receives diagnostics. Nothing derived from a client message is ever
	// passed to it — see the rule at logRefusal.
	log         *slog.Logger
	callTimeout time.Duration
}

// server is one service's broker.
type server struct {
	cfg serverConfig
}

// newServer validates the configuration and refuses anything it cannot enforce.
//
// The service must be one the policy set actually holds. A broker listening for
// a service with no policy document would deny every call, which sounds safe and
// is not: it looks like a working socket, so the failure surfaces as an agent
// that cannot do anything rather than as a missing grant.
func newServer(cfg serverConfig) (*server, error) {
	if err := mcpbroker.ValidateServiceName(cfg.service); err != nil {
		return nil, err
	}
	if cfg.upstream == nil {
		return nil, errors.New("broker needs an upstream")
	}
	if cfg.audit == nil {
		return nil, errors.New("broker needs an audit sink: a decision that cannot be recorded must not be taken")
	}
	if cfg.log == nil {
		return nil, errors.New("broker needs a logger")
	}
	if cfg.peerUID == nil {
		cfg.peerUID = peerUID
	}
	if cfg.callTimeout <= 0 {
		cfg.callTimeout = defaultUpstreamTimeout
	}
	if cfg.policyPath == "" {
		return nil, errors.New("broker needs the path of the policy document, so a denial can name it")
	}
	if !holdsService(cfg.policy, cfg.service) {
		return nil, fmt.Errorf("no policy document for service %q: the broker does not open a socket it has no grant for", cfg.service)
	}
	return &server{cfg: cfg}, nil
}

// holdsService reports whether the policy set carries a grant for service.
func holdsService(set mcpbroker.Set, service string) bool {
	for _, sg := range set.Grants().Services {
		if sg.Name == service {
			return true
		}
	}
	return false
}

// serve accepts connections until ctx is cancelled.
//
// Every connection gets its own goroutine. That is the whole of the isolation
// between clients: one client's slow tool call parks its own goroutine in an
// upstream round trip and nothing else, so it cannot delay another client's
// request or the accept loop.
func (s *server) serve(ctx context.Context, ln *net.UnixListener) error {
	// The two defers below are ordered, and the order is the whole point. Go runs
	// them last-first, so the connections are cancelled and only then joined. The
	// other way round is a deadlock: an MCP session ends when its client says so,
	// and if the listener has failed nobody is going to tell the clients.
	var conns sync.WaitGroup
	defer conns.Wait()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Closing the listener is what unblocks Accept; a deadline would only make
	// the loop spin. The watcher is released on every return path.
	released := make(chan struct{})
	defer close(released)
	go func() {
		select {
		case <-ctx.Done():
			ln.Close()
		case <-released:
		}
	}()

	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				// The listener was closed on purpose. A requested shutdown is not a
				// failure.
				return nil
			}
			return fmt.Errorf("accept on %s: %w", ln.Addr(), err)
		}
		conns.Add(1)
		go func() {
			defer conns.Done()
			s.handleConn(ctx, conn)
		}()
	}
}

// handleConn serves one client until it hangs up or the broker shuts down.
func (s *server) handleConn(ctx context.Context, conn *net.UnixConn) {
	defer conn.Close()

	// Shutdown has to reach a connection parked in a read. An MCP session is idle
	// almost all of the time, so waiting for one to end on its own would mean the
	// broker never stops.
	released := make(chan struct{})
	defer close(released)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-released:
		}
	}()

	// The uid is read once, before a single byte is served. A connection whose
	// caller the kernel will not name cannot be audited, and a call that cannot be
	// audited must not be answered (ADR-0004 §5).
	uid, err := s.cfg.peerUID(conn)
	if err != nil {
		s.logRefusal("peer credentials unavailable; connection refused")
		return
	}

	lines := bufio.NewScanner(conn)
	lines.Buffer(make([]byte, 0, readBufferBytes), maxRequestBytes)
	for lines.Scan() {
		// The scanner reuses its buffer, so the line is copied before it is used:
		// the copy is what the upstream is handed, and an implementation that
		// retained the scanner's slice would see it change under it. The copy lives
		// for one message and is dropped with it.
		line := append([]byte(nil), lines.Bytes()...)
		if !s.handleMessage(ctx, conn, uid, line) {
			return
		}
	}
	if err := lines.Err(); err != nil {
		s.endOfStream(conn, err)
	}
}

// endOfStream reports why a connection stopped producing messages.
func (s *server) endOfStream(conn *net.UnixConn, err error) {
	if errors.Is(err, bufio.ErrTooLong) {
		// Framing is lost: the oversized line was consumed in pieces and there is no
		// way to find where the next message begins. Answering and then closing is
		// the only honest end — continuing would mean reading the remains of one
		// message as if it were another.
		s.write(conn, func(w io.Writer) error {
			return writeError(w, nullID, codeInvalidRequest,
				fmt.Sprintf("request exceeds the %d-byte limit the broker accepts; it was refused, not truncated", maxRequestBytes))
		})
		s.logRefusal("oversized request refused; connection closed")
		return
	}
	// A read error on a unix socket is the client going away, which is ordinary.
	s.cfg.log.Debug("client connection ended", "service", s.cfg.service)
}

// handleMessage handles one client message and reports whether the connection
// may carry another.
func (s *server) handleMessage(ctx context.Context, conn *net.UnixConn, uid uint32, line []byte) bool {
	req, code, err := parseRequest(line)
	if err != nil {
		// err describes bytes the caller chose, so it is not rendered anywhere. The
		// client gets a fixed sentence per failure class; the log gets no part of
		// the message at all.
		s.logRefusal("malformed request refused")
		return s.write(conn, func(w io.Writer) error {
			return writeError(w, req.ID, code, refusalMessage(code))
		})
	}

	// A message's shape is checked before its content. A tools/call without an id
	// is the case that matters: read as a notification it would be carried
	// upstream with its result thrown away, and read as a request it would be
	// answered on a stream where the client is not expecting an answer. Neither is
	// a guess worth making.
	switch req.Method {
	case methodToolsCall, methodToolsList, methodInitialize, methodPing:
		if req.isNotification() {
			s.logRefusal("a request method arrived without an id; refused")
			return s.write(conn, func(w io.Writer) error {
				return writeError(w, nullID, codeInvalidRequest, missingID)
			})
		}
	case methodInitialized:
		if !req.isNotification() {
			return s.write(conn, func(w io.Writer) error {
				return writeError(w, req.ID, codeInvalidRequest, unexpectedID)
			})
		}
	default:
		if req.isNotification() {
			// A notification gets no reply even when it is refused: JSON-RPC forbids
			// one, and a message the client is not waiting for would be read as the
			// answer to whatever it asks next.
			s.logRefusal("an ungoverned notification was dropped")
			return true
		}
		return s.write(conn, func(w io.Writer) error {
			return writeError(w, req.ID, codeMethodNotFound, ungovernedMethod)
		})
	}

	switch req.Method {
	case methodToolsCall:
		return s.handleToolsCall(ctx, conn, uid, req, line)
	case methodToolsList:
		return s.handleToolsList(ctx, conn, req, line)
	default:
		// initialize, ping and notifications/initialized are the session itself
		// rather than anything the policy speaks about. They are carried unchanged:
		// a broker that answered them locally would be describing an upstream it
		// has not spoken to.
		return s.forward(ctx, conn, req, line)
	}
}

// missingID and unexpectedID are the two ways a message can be the wrong shape
// for the method it names.
const (
	missingID    = "this method is a request and must carry a string or numeric id; the broker will not carry a call whose result has nowhere to go"
	unexpectedID = "notifications/initialized is a notification and must not carry an id"
)

// MCP methods the broker recognises. Everything else is refused; see
// ungovernedMethod.
const (
	methodToolsCall   = "tools/call"
	methodToolsList   = "tools/list"
	methodInitialize  = "initialize"
	methodInitialized = "notifications/initialized"
	methodPing        = "ping"
)

// handleToolsList is the enforcement point for visibility: what the client can
// see must equal what it may call.
//
// The listing is not audited. An audit record is a decision about a call, and
// nothing was called; one deny line per hidden tool, on every listing, would bury
// the denials that mean something under a stream that means nothing.
func (s *server) handleToolsList(ctx context.Context, conn *net.UnixConn, req rpcRequest, line []byte) bool {
	reply, err := s.roundTrip(ctx, line)
	if err != nil {
		s.cfg.log.Error("upstream call failed", "service", s.cfg.service, "error", err)
		return s.write(conn, func(w io.Writer) error {
			return writeError(w, req.ID, codeInternalError, upstreamFailed)
		})
	}

	filtered, err := s.filterToolsList(reply)
	if err != nil {
		// Withholding the whole listing is the only safe failure. Passing an
		// unfiltered one through would publish tools the policy never granted, and
		// dropping the entries that could not be read would hide that the upstream
		// is sending something nobody understands.
		s.cfg.log.Error("upstream tools/list could not be filtered; the listing was withheld",
			"service", s.cfg.service, "error", err)
		return s.write(conn, func(w io.Writer) error {
			return writeError(w, req.ID, codeInternalError, unfilterableListing)
		})
	}
	return s.write(conn, func(w io.Writer) error { return writeFramed(w, filtered) })
}

// unfilterableListing is what a client is told when the granted surface could
// not be computed.
const unfilterableListing = "the broker could not filter the upstream tool listing to the granted tools and withheld it; " +
	"a listing it cannot filter is one it cannot vouch for"

// toolsListEnvelope is the response envelope the filter rebuilds.
type toolsListEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
}

// filterToolsList removes from an upstream listing every tool the policy does
// not grant.
//
// Entries that survive are passed through as they arrived. The broker decides
// which tools the client sees; it does not describe them. A rebuilt entry would
// be a schema Torio wrote, and the client would call the tool according to it.
//
// Every error here is a fixed sentence. The reply is upstream content, so no part
// of it may end up in an error that gets logged (ADR-0004 §5).
func (s *server) filterToolsList(reply []byte) ([]byte, error) {
	var env struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(reply, &env); err != nil {
		return nil, errors.New("upstream reply is not a JSON-RPC message")
	}
	if len(env.Error) > 0 {
		// An upstream error carries no tool listing, so there is nothing to filter
		// and nothing gained by rewriting it.
		return reply, nil
	}
	if env.JSONRPC != jsonrpcVersion {
		return nil, errors.New("upstream reply does not declare jsonrpc 2.0")
	}
	if len(env.ID) == 0 {
		return nil, errors.New("upstream reply carries no id")
	}
	if len(env.Result) == 0 {
		return nil, errors.New("upstream reply carries neither result nor error")
	}

	// The result is kept as a map so members the broker does not know about —
	// nextCursor today, whatever pagination or metadata MCP adds next — survive
	// untouched. Only "tools" is replaced.
	var result map[string]json.RawMessage
	if err := json.Unmarshal(env.Result, &result); err != nil {
		return nil, errors.New("upstream tools/list result is not an object")
	}
	rawTools, ok := result["tools"]
	if !ok {
		return nil, errors.New("upstream tools/list result has no tools member")
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(rawTools, &entries); err != nil {
		return nil, errors.New("upstream tools/list tools member is not an array")
	}

	granted := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		var named struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(entry, &named); err != nil || named.Name == "" {
			return nil, errors.New("upstream tools/list holds an entry with no name; a tool that cannot be named cannot be decided about")
		}
		if s.cfg.policy.Allow(s.cfg.service, named.Name).Allowed {
			granted = append(granted, entry)
		}
	}

	tools, err := json.Marshal(granted)
	if err != nil {
		return nil, errors.New("filtered tool listing could not be encoded")
	}
	result["tools"] = tools
	body, err := json.Marshal(result)
	if err != nil {
		return nil, errors.New("filtered tools/list result could not be encoded")
	}
	out, err := json.Marshal(toolsListEnvelope{JSONRPC: env.JSONRPC, ID: env.ID, Result: body})
	if err != nil {
		// Not err: an encoder error over raw upstream bytes quotes the byte it
		// tripped on, and the rule is that no part of a reply reaches a log.
		return nil, errors.New("filtered tools/list reply could not be encoded")
	}
	return out, nil
}

// handleToolsCall is the enforcement point for invocation: no tool runs upstream
// unless the policy document lists it by name.
func (s *server) handleToolsCall(ctx context.Context, conn *net.UnixConn, uid uint32, req rpcRequest, line []byte) bool {
	name, err := toolName(req.Params)
	if err != nil {
		s.logRefusal("tool call with unreadable params refused")
		return s.write(conn, func(w io.Writer) error {
			return writeError(w, req.ID, codeInvalidParams,
				`tools/call params must be an object with a "name" string; the broker will not guess which tool was meant`)
		})
	}

	decision := s.cfg.policy.Allow(s.cfg.service, name)

	// The record is written before the call is carried out, and a call that
	// cannot be recorded is not carried out at all. An audit trail assembled after
	// the fact is missing exactly the calls that crashed the process, and those
	// are the ones anybody would want.
	if err := s.cfg.audit.record(mcpbroker.AuditRecord{
		Time:    time.Now(),
		Service: s.cfg.service,
		Tool:    name,
		UID:     uid,
		Allowed: decision.Allowed,
		Reason:  decision.Reason,
	}); err != nil {
		s.cfg.log.Error("could not record a decision; the call was refused", "service", s.cfg.service, "error", err)
		return s.write(conn, func(w io.Writer) error {
			return writeError(w, req.ID, codeInternalError, auditUnavailable)
		})
	}

	if !decision.Allowed {
		return s.write(conn, func(w io.Writer) error {
			return writeError(w, req.ID, codeDenied, s.denialMessage(name))
		})
	}

	return s.forward(ctx, conn, req, line)
}

// forward carries one message upstream and returns the reply unchanged.
//
// Unchanged is the requirement, not an optimisation: the reply is upstream
// content, and the only thing the broker is entitled to do with it is hand it to
// the client that was allowed to ask. It is not read, not summarised and not
// logged.
func (s *server) forward(ctx context.Context, conn *net.UnixConn, req rpcRequest, line []byte) bool {
	reply, err := s.roundTrip(ctx, line)
	if err != nil {
		// The error is the transport's own, never a fragment of a reply body — the
		// contract on the upstream interface makes that the implementation's
		// obligation, because this is the one place an upstream error is written
		// down.
		s.cfg.log.Error("upstream call failed", "service", s.cfg.service, "error", err)
		return s.write(conn, func(w io.Writer) error {
			return writeError(w, req.ID, codeInternalError, upstreamFailed)
		})
	}
	if req.isNotification() {
		// Nothing may be sent back for a notification, and a reply to one is the
		// upstream's protocol error, not something to relay into the client's
		// stream where it would be read as an answer to a later request.
		return true
	}
	if len(reply) == 0 {
		s.cfg.log.Error("upstream returned no reply to a request", "service", s.cfg.service)
		return s.write(conn, func(w io.Writer) error {
			return writeError(w, req.ID, codeInternalError, upstreamFailed)
		})
	}
	return s.write(conn, func(w io.Writer) error { return writeFramed(w, reply) })
}

// roundTrip bounds one upstream call. The deadline is derived from the
// connection's context, so a shutdown cancels an in-flight call instead of
// waiting out its timeout.
func (s *server) roundTrip(ctx context.Context, line []byte) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.callTimeout)
	defer cancel()
	return s.cfg.upstream.roundTrip(ctx, line)
}

// upstreamFailed is what a client is told when a call did not complete. It
// carries no detail on purpose: the detail is the upstream's, and the client is
// the party the broker mediates.
const upstreamFailed = "the broker could not complete the call upstream; the reason is in the broker's log, not in this reply"

// auditUnavailable is what a client is told when the decision could not be
// written down. The call is refused whichever way the decision went: an
// unrecorded allow is a call nobody can account for afterwards, and accounting
// is what the broker is for (ADR-0004 §5).
const auditUnavailable = "the broker could not record this decision and therefore did not act on it; the broker's log has the reason"

// denialMessage names what was refused, where the grant lives, and who can
// change it. ADR-0004 makes the granted surface legible on purpose; a denial is
// the moment that legibility is worth something, so it points at the document
// rather than restating that the answer is no.
func (s *server) denialMessage(tool string) string {
	return fmt.Sprintf("tool %q is not granted to the %q MCP service: the broker forwards only the tools listed in %s, "+
		"which is root-owned and readable by everyone — an operator grants a tool there and restarts the broker",
		boundToolName(tool), s.cfg.service, s.cfg.policyPath)
}

// maxEchoedToolNameLen bounds a caller-chosen tool name echoed back in a
// denial. It matches the bound mcpbroker applies to the same name in the audit
// line: a message is a place the caller should not be able to choose the size of.
const maxEchoedToolNameLen = 128

func boundToolName(name string) string {
	if len(name) <= maxEchoedToolNameLen {
		return name
	}
	return name[:maxEchoedToolNameLen] + "…"
}

// toolCallParams is the only part of a tools/call the broker reads.
//
// There is deliberately no field for arguments. The arguments are Jira and
// Confluence content on their way upstream; the broker forwards the line it
// received and never decodes them into a value it owns, so there is nowhere in
// this process they could be logged from (ADR-0004 §5).
type toolCallParams struct {
	Name string `json:"name"`
}

// toolName extracts the tool a call names.
func toolName(params json.RawMessage) (string, error) {
	if len(params) == 0 {
		return "", errors.New("tools/call carries no params")
	}
	var p toolCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", errors.New("tools/call params are not an object")
	}
	if p.Name == "" {
		return "", errors.New("tools/call names no tool")
	}
	return p.Name, nil
}

// ungovernedMethod is what the broker says to a method it will not carry.
//
// The refusal is not "unknown method". Every other MCP surface — resources,
// prompts, sampling, completion — is refused because a policy document grants
// tools by name (ADR-0004 §4) and has no vocabulary for anything else. A surface
// the grant cannot describe is one the broker cannot enforce, so it is refused
// rather than proxied, and the sentence says so: an operator who reads "method
// not found" goes looking for a typo instead of for the decision.
const ungovernedMethod = "the Torio MCP broker carries only initialize, ping, tools/list and tools/call; " +
	"a policy document grants tools by name and cannot describe any other MCP surface, " +
	"so the rest is refused rather than proxied"

// refusalMessage is the client-visible sentence for a refusal class. The set is
// closed and every sentence is written here, so no client can influence what the
// broker says back to it beyond choosing which of these it gets.
func refusalMessage(code int) string {
	switch code {
	case codeParseError:
		return "message is not a JSON-RPC object; the broker refuses a message it cannot parse rather than guessing at it"
	case codeInvalidRequest:
		return "message is not a valid JSON-RPC 2.0 request: it must declare jsonrpc \"2.0\", a method, and a string or numeric id"
	default:
		return "request refused"
	}
}

// write sends one message with a deadline and reports whether the connection is
// still usable.
//
// The deadline bounds a client that stops reading. Without it, that client's
// goroutine parks in a write forever; with it, the connection is dropped and the
// broker keeps serving everyone else.
func (s *server) write(conn *net.UnixConn, send func(io.Writer) error) bool {
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return false
	}
	if err := send(conn); err != nil {
		s.logRefusal("could not send a response; connection closed")
		return false
	}
	return true
}

// logRefusal writes a diagnostic that names the service and nothing else.
//
// This is the rule the whole file obeys: no value derived from a client message
// reaches the log. Not the method, not the tool name, not the parser's
// complaint. The audit log is the one place a caller-supplied name is recorded,
// and mcpbroker.WriteAudit bounds and escapes it there. Everything else the
// client sends is content, and content does not get written down (ADR-0004 §5).
func (s *server) logRefusal(msg string) {
	s.cfg.log.Warn(msg, "service", s.cfg.service)
}
