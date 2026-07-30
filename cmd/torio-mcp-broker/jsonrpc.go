package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// jsonrpcVersion is the only version this broker speaks. MCP pins JSON-RPC 2.0,
// and a message declaring anything else is refused rather than interpreted: the
// broker decides what a message is allowed to do, so it must not have to guess
// what the message means.
const jsonrpcVersion = "2.0"

// JSON-RPC error codes. The first four are the standard ones; codeDenied is in
// the -32000..-32099 range JSON-RPC 2.0 reserves for implementation-defined
// server errors, because neither JSON-RPC nor MCP defines a code for "the
// gateway would not carry this call".
//
// A policy denial is deliberately a protocol-level error rather than an MCP tool
// result with isError set. A tool result would read as the tool having run and
// failed; nothing ran, and the difference is the whole point of the record.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
	codeDenied         = -32001
)

// rpcRequest is the envelope of one client message, and only the envelope.
//
// Params stays raw. Decoding it here would mean holding tool call arguments —
// Jira and Confluence content — in a structure this process owns, and ADR-0022
// §5 keeps that content out of every Torio surface. Only the two enforcement
// points look inside, and each looks for exactly one field.
//
// Unknown fields are not rejected: an MCP request may carry more than this
// broker reads, and the whole line is forwarded verbatim, so refusing what we do
// not parse would break calls the policy grants.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// isNotification reports whether the message expects no reply. JSON-RPC marks a
// notification by the absence of an id, not by a null one.
func (r rpcRequest) isNotification() bool { return len(r.ID) == 0 }

// nullID is the id of a response to a message whose own id could not be read.
var nullID = json.RawMessage("null")

// parseRequest reads one line as a JSON-RPC request.
//
// It returns whatever id it managed to extract even when it rejects the message,
// so a client that sent a structurally wrong request still gets an answer it can
// correlate. The returned error is never rendered to the client or the log: it
// describes bytes the caller chose, and both destinations are places the caller
// must not be able to write to (ADR-0022 §5).
func parseRequest(line []byte) (rpcRequest, int, error) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return req, codeParseError, errors.New("message is not a JSON object")
	}
	if req.JSONRPC != jsonrpcVersion {
		return req, codeInvalidRequest, errors.New("message does not declare jsonrpc 2.0")
	}
	if req.Method == "" {
		return req, codeInvalidRequest, errors.New("message declares no method")
	}
	if !req.isNotification() && !isScalarID(req.ID) {
		// A request id must be a string or a number. Refusing anything else is not
		// pedantry: the id is echoed back into every response the broker writes, so
		// admitting an arbitrary structure would make the caller the author of part
		// of the broker's output.
		req.ID = nil
		return req, codeInvalidRequest, errors.New("request id is neither a string nor a number")
	}
	return req, 0, nil
}

// isScalarID reports whether raw is a JSON string or number. MCP forbids a null
// id, so null is not accepted either.
func isScalarID(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	switch v.(type) {
	case string, float64:
		return true
	default:
		return false
	}
}

// rpcError is the error member of a response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcErrorResponse is a complete error response.
type rpcErrorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   rpcError        `json:"error"`
}

// writeError sends one error response.
//
// message is written by this binary and never assembled from an upstream reply:
// an upstream error string can carry a fragment of the body that produced it,
// and the broker must not become the path by which one client's content reaches
// another surface.
func writeError(w io.Writer, id json.RawMessage, code int, message string) error {
	if len(id) == 0 {
		id = nullID
	}
	return writeMessage(w, rpcErrorResponse{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Error:   rpcError{Code: code, Message: message},
	})
}

// writeMessage marshals v and writes it as one framed line.
//
// json.Marshal escapes every control byte inside a string, so no value carried
// in a message can end the line early and inject a second one. That matters
// because some of those values — a denied tool name, a request id — came from the
// caller.
func writeMessage(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeLine(w, data)
}

// writeLine writes one already-encoded message as a line.
func writeLine(w io.Writer, data []byte) error {
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// writeFramed writes an upstream reply as one framed line.
//
// Compacting is framing, not rewriting: a pretty-printed reply carries newlines,
// and a newline inside a message would split it into two on the way to a client
// that reads one message per line. json.Compact removes only whitespace between
// tokens and never touches a string's contents, so what the client receives is
// still the upstream's own message.
//
// It also fails on a reply that is not valid JSON, which is the behaviour to
// want: a broker that relayed unparsable bytes would be handing the client
// something it never checked was a message at all.
func writeFramed(w io.Writer, reply []byte) error {
	var compact bytes.Buffer
	if err := json.Compact(&compact, reply); err != nil {
		return fmt.Errorf("upstream reply is not JSON: %w", err)
	}
	return writeLine(w, compact.Bytes())
}
