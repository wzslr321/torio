package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

type pipeHalfConn struct {
	response *io.PipeReader
	request  *io.PipeWriter
}

func (c *pipeHalfConn) Read(p []byte) (int, error)  { return c.response.Read(p) }
func (c *pipeHalfConn) Write(p []byte) (int, error) { return c.request.Write(p) }
func (c *pipeHalfConn) CloseWrite() error           { return c.request.Close() }
func (c *pipeHalfConn) Close() error {
	_ = c.request.Close()
	return c.response.Close()
}

func TestSocketPathRejectsAnythingButPolicyServiceSlug(t *testing.T) {
	for _, service := range []string{"", "Atlassian", "../tickets", "tickets.sock", "a/b", strings.Repeat("a", 33)} {
		if path, err := socketPath("/run/torio-mcp", service); err == nil {
			t.Errorf("socketPath(%q) = %q, want rejection", service, path)
		}
	}
	got, err := socketPath("/run/torio-mcp", "tickets")
	if err != nil || got != "/run/torio-mcp/tickets.sock" {
		t.Fatalf("socketPath(tickets) = %q, %v", got, err)
	}
}

func TestRelayCarriesBytesBothWaysAfterStdinEOF(t *testing.T) {
	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	conn := &pipeHalfConn{response: responseReader, request: requestWriter}

	serverDone := make(chan error, 1)
	go func() {
		defer requestReader.Close()
		defer responseWriter.Close()
		request, err := io.ReadAll(requestReader)
		if err == nil && string(request) != "request\n" {
			err = io.ErrUnexpectedEOF
		}
		if err == nil {
			_, err = responseWriter.Write([]byte("response\n"))
		}
		serverDone <- err
	}()

	var stdout bytes.Buffer
	if err := relay(context.Background(), conn, strings.NewReader("request\n"), &stdout); err != nil {
		t.Fatalf("relay: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
	if got := stdout.String(); got != "response\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunKeepsDiagnosticsOffProtocolStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"../bad"}, t.TempDir(), strings.NewReader(""), &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Fatalf("protocol stdout = %q, want empty", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("diagnostic stderr is empty")
	}
}
