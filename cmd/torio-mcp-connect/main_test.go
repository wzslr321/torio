package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"time"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

// The service name is the only untrusted input this binary has, and it is the
// input a socket path is built from. Every one of these must be rejected
// outright rather than cleaned up: a name that needs normalizing is a name the
// caller got wrong, and silently repairing it hides which socket was addressed.
func TestSocketPathRejectsNonSlugService(t *testing.T) {
	cases := []struct {
		name    string
		service string
	}{
		{"empty", ""},
		{"space", " "},
		{"leading space", " atlassian"},
		{"trailing space", "atlassian "},
		{"uppercase", "Atlassian"},
		{"underscore", "at_lassian"},
		{"leading hyphen", "-atlassian"},
		{"trailing hyphen", "atlassian-"},
		{"dot", "."},
		{"dot dot", ".."},
		{"inner dot", "a.b"},
		{"suffix included", "atlassian.sock"},
		{"traversal", "../../etc/shadow"},
		{"separator", "a/b"},
		{"absolute path", "/run/torio-mcp/atlassian"},
		{"trailing separator", "atlassian/"},
		{"nul byte", "atlassian\x00"},
		{"newline", "atlas\nsian"},
		{"non-ascii", "atlassián"},
		{"too long", strings.Repeat("a", mcpbroker.MaxServiceNameLen+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mcpbroker.SocketPath("/run/torio-mcp", tc.service)
			if err == nil {
				t.Fatalf("mcpbroker.SocketPath(%q) = %q, want rejection", tc.service, got)
			}
		})
	}
}

// tempSocketDir is a socket base short enough to survive sun_path, the kernel's
// ~104-byte limit on a unix socket address. t.TempDir() embeds the test name
// and on macOS overruns it, which surfaces as EINVAL from connect and would be
// read as a bug in the relay rather than in the fixture.
func tempSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tmc")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// Every accepted name must resolve to a direct child of the base directory.
// That is the property the rejection table exists to protect, so it is asserted
// on the result and not only on the input.
func TestSocketPathIsContainedInBase(t *testing.T) {
	const base = "/run/torio-mcp"
	cases := []struct {
		service string
		want    string
	}{
		{"atlassian", "/run/torio-mcp/atlassian.sock"},
		{"slack", "/run/torio-mcp/slack.sock"},
		{"a", "/run/torio-mcp/a.sock"},
		{"jira-cloud", "/run/torio-mcp/jira-cloud.sock"},
		{"s3", "/run/torio-mcp/s3.sock"},
		{strings.Repeat("a", mcpbroker.MaxServiceNameLen), "/run/torio-mcp/" + strings.Repeat("a", mcpbroker.MaxServiceNameLen) + ".sock"},
	}
	for _, tc := range cases {
		t.Run(tc.service, func(t *testing.T) {
			got, err := mcpbroker.SocketPath(base, tc.service)
			if err != nil {
				t.Fatalf("mcpbroker.SocketPath(%q): %v", tc.service, err)
			}
			if got != tc.want {
				t.Errorf("mcpbroker.SocketPath(%q) = %q, want %q", tc.service, got, tc.want)
			}
			if dir := filepath.Dir(got); dir != base {
				t.Errorf("mcpbroker.SocketPath(%q) resolved outside base: parent %q", tc.service, dir)
			}
		})
	}
}

// A usage error must never reach stdout: Hermes reads stdout as the MCP
// protocol stream, so a stray diagnostic byte there is a protocol violation,
// not just noise.
func TestRunRejectsWrongArity(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no arguments", nil},
		{"two arguments", []string{"atlassian", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tc.args, tempSocketDir(t), strings.NewReader(""), &stdout, &stderr)

			if code != exitUsage {
				t.Errorf("exit = %d, want %d", code, exitUsage)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), "usage:") {
				t.Errorf("stderr = %q, want a usage line", stderr.String())
			}
		})
	}
}

// A rejected name must fail the same way a bad arity does: exit 2, nothing on
// stdout, and a reason on stderr that names the rule instead of the errno the
// dial would eventually have produced.
func TestRunRejectsInvalidServiceName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"../../etc/shadow"}, tempSocketDir(t), strings.NewReader(""), &stdout, &stderr)

	if code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "lowercase slug") {
		t.Errorf("stderr = %q, want the accepted shape spelled out", stderr.String())
	}
}

// The three dial failures below are the ones an operator actually hits, and
// they have different remedies: install the broker, join the client group,
// start the unit. Collapsing them into one "connection error" is what makes a
// guest take an afternoon to debug, so each gets its own exit code and its own
// remediation sentence.
func TestRunReportsMissingSocket(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"atlassian"}, tempSocketDir(t), strings.NewReader(""), &stdout, &stderr)

	if code != exitNoBroker {
		t.Errorf("exit = %d, want %d", code, exitNoBroker)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if msg := stderr.String(); !strings.Contains(msg, "no broker socket") {
		t.Errorf("stderr = %q, want it to name the missing socket", msg)
	}
	if msg := stderr.String(); !strings.Contains(msg, "on the host") || strings.Contains(msg, "on the guest") {
		t.Errorf("stderr = %q, want the torio mcp install remedy on the host", msg)
	}
}

func TestRunReportsPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the socket mode, so the denial under test cannot happen")
	}
	dir := tempSocketDir(t)
	path := filepath.Join(dir, "atlassian.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	// Stands in for the real 0660 torio-mcp:torio-mcp-clients socket seen by an
	// identity outside the group: present, and not ours to open.
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"atlassian"}, dir, strings.NewReader(""), &stdout, &stderr)

	if code != exitDenied {
		t.Errorf("exit = %d, want %d", code, exitDenied)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if msg := stderr.String(); !strings.Contains(msg, "torio-mcp-clients") {
		t.Errorf("stderr = %q, want it to name the group that grants access", msg)
	}
}

// TestPermissionDeniedNamesBothCauses: EACCES on this path has two causes, and
// the relay cannot tell them apart from where it stands.
//
// One is the socket's own 0660 torio-mcp:torio-mcp-clients — this identity is
// outside the group. The other is the directory above it: at 0750 the socket is
// reachable only by traversing /run/torio-mcp, so a directory handed to the
// wrong group denies a caller who *is* a member. Naming only the first sends an
// operator to check a group that is already correct.
func TestPermissionDeniedNamesBothCauses(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the socket mode, so the denial under test cannot happen")
	}
	dir := tempSocketDir(t)
	path := filepath.Join(dir, "atlassian.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	var stdout, stderr bytes.Buffer
	run(context.Background(), []string{"atlassian"}, dir, strings.NewReader(""), &stdout, &stderr)

	msg := stderr.String()
	if !strings.Contains(msg, "torio-mcp-clients") {
		t.Errorf("stderr = %q, want it to name the group that grants access", msg)
	}
	if !strings.Contains(msg, mcpbroker.SocketDir) {
		t.Errorf("stderr = %q, want it to name %s, the other thing that produces this error", msg, mcpbroker.SocketDir)
	}
}

func TestRunReportsConnectionRefused(t *testing.T) {
	dir := tempSocketDir(t)
	path := filepath.Join(dir, "atlassian.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Leaving the file behind is the point: this is the state a crashed broker
	// leaves on disk, and it must not be reported as "not installed".
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"atlassian"}, dir, strings.NewReader(""), &stdout, &stderr)

	if code != exitBrokerDown {
		t.Errorf("exit = %d, want %d", code, exitBrokerDown)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if msg := stderr.String(); !strings.Contains(msg, "nothing is listening") {
		t.Errorf("stderr = %q, want it to distinguish a stale socket from a missing one", msg)
	}
}

// listen puts a fake broker on the socket for one connection and returns the
// bytes it received. handle runs after the request side reaches EOF, which is
// what makes this a test of the half-close and not only of the copy.
func listen(t *testing.T, path string, handle func(conn *net.UnixConn)) <-chan []byte {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	received := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(received)
			return
		}
		defer conn.Close()
		unix := conn.(*net.UnixConn)
		got, err := io.ReadAll(unix)
		if err != nil {
			close(received)
			return
		}
		received <- got
		handle(unix)
	}()
	return received
}

// The response is the reason this binary is careful about shutdown. Hermes
// closes stdin when it has finished a request; if that were treated as "the
// session is over", the relay would cut the broker off mid-answer and Hermes
// would see a truncated MCP frame. So stdin EOF must half-close the socket —
// enough for the broker to see the request end — and the inbound direction must
// keep running until the broker itself is done.
func TestRunDrainsBrokerResponseAfterStdinCloses(t *testing.T) {
	dir := tempSocketDir(t)
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"
	// Larger than any pipe or socket buffer, so a response that survives whole
	// proves the drain rather than a lucky single write.
	response := bytes.Repeat([]byte("mcp-response-frame-"), 100_000)

	received := listen(t, filepath.Join(dir, "atlassian.sock"), func(conn *net.UnixConn) {
		if _, err := conn.Write(response); err != nil {
			t.Errorf("broker write: %v", err)
		}
	})

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"atlassian"}, dir, strings.NewReader(request), &stdout, &stderr)

	if code != exitOK {
		t.Errorf("exit = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if got := <-received; string(got) != request {
		t.Errorf("broker received %q, want %q", got, request)
	}
	if got := stdout.Bytes(); !bytes.Equal(got, response) {
		t.Errorf("stdout is %d bytes, want the whole %d-byte response", len(got), len(response))
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty on a clean session", stderr.String())
	}
}

// MCP over stdio is a conversation, not a single round trip: Hermes keeps
// sending while the broker keeps answering. A relay that only forwarded the
// request and then read the reply would pass the drain test above and deadlock
// here on the second exchange, which is why both exist.
func TestRunRelaysWhileBothSidesAreOpen(t *testing.T) {
	dir := tempSocketDir(t)
	ln, err := net.Listen("unix", filepath.Join(dir, "atlassian.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		lines := bufio.NewScanner(conn)
		for lines.Scan() {
			fmt.Fprintf(conn, "reply to %s\n", lines.Text())
		}
	}()

	// net.Pipe rather than io.Pipe: it takes deadlines, so a relay that stops
	// pumping fails in seconds instead of hanging the suite.
	stdinR, stdinW := net.Pipe()
	stdoutR, stdoutW := net.Pipe()
	deadline := time.Now().Add(10 * time.Second)
	for _, c := range []net.Conn{stdinW, stdoutR} {
		if err := c.SetDeadline(deadline); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
	}

	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(context.Background(), []string{"atlassian"}, dir, stdinR, stdoutW, &stderr)
		stdoutW.Close()
	}()

	replies := bufio.NewReader(stdoutR)
	for i := 1; i <= 3; i++ {
		if _, err := fmt.Fprintf(stdinW, "request-%d\n", i); err != nil {
			t.Fatalf("write request %d: %v", i, err)
		}
		got, err := replies.ReadString('\n')
		if err != nil {
			t.Fatalf("read reply %d: %v", i, err)
		}
		if want := fmt.Sprintf("reply to request-%d\n", i); got != want {
			t.Errorf("reply %d = %q, want %q", i, got, want)
		}
	}

	// Hermes closing stdin is the ordinary end of an MCP session.
	stdinW.Close()
	if code := <-done; code != exitOK {
		t.Errorf("exit = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty on a clean session", stderr.String())
	}
}

// SIGINT/SIGTERM must end the session even though both sides are idle and
// neither will ever return from its read on its own. Being asked to stop is not
// a failure, so it is not reported as one.
func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	dir := tempSocketDir(t)
	ln, err := net.Listen("unix", filepath.Join(dir, "atlassian.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	// A broker that accepts and then says nothing: the state a long-lived MCP
	// session spends nearly all its time in.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		<-release
	}()

	stdinR, stdinW := net.Pipe()
	defer stdinW.Close()

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"atlassian"}, dir, stdinR, &stdout, &stderr) }()

	<-accepted
	cancel()

	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("exit = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after its context was cancelled")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty: a requested shutdown is not a failure", stderr.String())
	}
}

// On the ordinary path — Hermes closes stdin, the broker answers and hangs up —
// every goroutine the relay started must be gone by the time it returns. The
// one that is easy to get wrong is the context watcher: it waits on a signal
// that will never arrive, so if the relay forgets to release it, it lives for
// as long as the process does.
func TestRelayLeavesNothingRunningAfterACleanSession(t *testing.T) {
	// Sampled before the fixture starts its own goroutine, so that goroutine's
	// exit cannot offset a leaked one and hide it.
	before := runtime.NumGoroutine()

	dir := tempSocketDir(t)
	received := listen(t, filepath.Join(dir, "atlassian.sock"), func(conn *net.UnixConn) {
		if _, err := conn.Write([]byte("ok\n")); err != nil {
			t.Errorf("broker write: %v", err)
		}
	})

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"atlassian"}, dir, strings.NewReader("hello\n"), &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	<-received

	// The count is polled rather than sampled: the fixture's own goroutine
	// finishes on its own schedule, and a single reading would be a coin flip.
	deadline := time.Now().Add(5 * time.Second)
	for {
		after := runtime.NumGoroutine()
		if after <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines settled at %d, started at %d", after, before)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSocketPathFitsSunPath makes the address-length argument executable rather
// than a comment. A unix socket address is bounded by sun_path (~104 bytes on
// darwin, 108 on linux); the service-name bound is what keeps the longest
// resolvable path inside it. If either the directory or the bound grows, this
// fails here instead of as an EINVAL from connect() on a machine somebody is
// trying to debug.
func TestSocketPathFitsSunPath(t *testing.T) {
	const sunPathLimit = 104
	longest := strings.Repeat("a", mcpbroker.MaxServiceNameLen)
	path, err := mcpbroker.SocketPath(mcpbroker.SocketDir, longest)
	if err != nil {
		t.Fatalf("socketPath rejected the longest accepted name: %v", err)
	}
	if len(path)+1 > sunPathLimit { // +1 for the NUL the kernel stores
		t.Errorf("longest socket path is %d bytes, over the %d-byte sun_path limit: %s",
			len(path)+1, sunPathLimit, path)
	}
}
