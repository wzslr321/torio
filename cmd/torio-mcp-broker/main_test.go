package main

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

// ownGroupName is a group the test process really belongs to, so the group
// lookup under test resolves the way it will on a guest where torio-mcp-clients
// exists.
func ownGroupName(t *testing.T) string {
	t.Helper()
	g, err := user.LookupGroupId(strconv.Itoa(os.Getgid()))
	if err != nil {
		t.Skipf("this machine cannot name gid %d: %v", os.Getgid(), err)
	}
	return g.Name
}

// dialSocket opens one client on a socket the daemon published.
func dialSocket(t *testing.T, path string) *client {
	t.Helper()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	return &client{conn: conn, r: bufio.NewReader(conn)}
}

// daemonFixture is a broker started through run, the way the unit starts it.
type daemonFixture struct {
	policyDir string
	socketDir string
	audit     *syncBuffer
	stderr    *syncBuffer
	exit      chan int
	cancel    context.CancelFunc
}

func writeDocument(t *testing.T, dir, name, doc string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(doc), 0o644); err != nil {
		t.Fatalf("write policy document: %v", err)
	}
}

// startDaemon runs the daemon in the background and returns once it has either
// published its sockets or exited.
func startDaemon(t *testing.T, policyDir string) *daemonFixture {
	t.Helper()
	f := &daemonFixture{
		policyDir: policyDir,
		socketDir: shortTempDir(t),
		audit:     &syncBuffer{},
		stderr:    &syncBuffer{},
		exit:      make(chan int, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	go func() {
		f.exit <- run(ctx, nil, daemonConfig{
			policyDir:   policyDir,
			socketDir:   f.socketDir,
			clientGroup: ownGroupName(t),
			// The daemon reads the caller's uid from the kernel, which only Linux
			// offers; the wiring still has to be exercised on a maintainer's machine,
			// so the source is injected here and nowhere else.
			peerUID: func(*net.UnixConn) (uint32, error) { return testUID, nil },
			stdout:  f.audit,
			stderr:  f.stderr,
		})
		// Closing after the send lets both the test body and the cleanup wait on
		// the same channel: whoever gets there second sees a closed channel rather
		// than blocking for a value that was already taken.
		close(f.exit)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-f.exit:
		case <-time.After(10 * time.Second):
			t.Error("run did not return after its context was cancelled")
		}
	})
	return f
}

func TestRunNotifiesSystemdOnlyAfterPublishingSockets(t *testing.T) {
	policyDir := t.TempDir()
	writeDocument(t, policyDir, "atlassian.json", policyDocument)
	socketDir := shortTempDir(t)
	notifyPath := filepath.Join(shortTempDir(t), "notify.sock")
	notifyListener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: notifyPath, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen for systemd readiness: %v", err)
	}
	defer notifyListener.Close()
	t.Setenv("NOTIFY_SOCKET", notifyPath)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	clientGroup := ownGroupName(t)
	go func() {
		done <- run(ctx, nil, daemonConfig{
			policyDir:   policyDir,
			socketDir:   socketDir,
			clientGroup: clientGroup,
			peerUID:     func(*net.UnixConn) (uint32, error) { return testUID, nil },
			stdout:      &syncBuffer{},
			stderr:      &syncBuffer{},
		})
	}()

	if err := notifyListener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set readiness deadline: %v", err)
	}
	buf := make([]byte, 64)
	n, _, err := notifyListener.ReadFromUnix(buf)
	if err != nil {
		cancel()
		t.Fatalf("read systemd readiness: %v", err)
	}
	if string(buf[:n]) != "READY=1" {
		cancel()
		t.Fatalf("readiness payload = %q, want READY=1", buf[:n])
	}
	if _, err := os.Lstat(filepath.Join(socketDir, "atlassian.sock")); err != nil {
		cancel()
		t.Fatalf("readiness arrived before the service socket existed: %v", err)
	}
	loaded, err := mcpbroker.Load(policyDir)
	if err != nil {
		cancel()
		t.Fatalf("load expected policy: %v", err)
	}
	digest, err := os.ReadFile(filepath.Join(socketDir, policyDigestFile))
	if err != nil {
		cancel()
		t.Fatalf("read published policy digest: %v", err)
	}
	if got, want := string(digest), loaded.Digest()+"\n"; got != want {
		cancel()
		t.Fatalf("published policy digest = %q, want %q", got, want)
	}
	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("run exit = %d, want %d", code, exitOK)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancellation")
	}
}

// The broker must not start without a policy it could load. A broker that came
// up "granting nothing" would look healthy — the unit is active, the socket is
// there, the mode is right — and every call would be denied for a reason nobody
// could see. Refusing to start is the loud failure.
func TestRunRefusesToStartWithoutALoadablePolicy(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T) string
		wantExit int
		wantSaid string
	}{
		{
			name: "document does not parse",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				writeDocument(t, dir, "atlassian.json", `{"schema_version":"1","service":"atlassian",`)
				return dir
			},
			wantExit: exitUsage,
			wantSaid: "policy",
		},
		{
			name: "document grants an unclassified tool",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				writeDocument(t, dir, "atlassian.json",
					`{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://mcp.example.invalid/v1","tools":[{"name":"deleteJiraIssue"}]}`)
				return dir
			},
			wantExit: exitUsage,
			wantSaid: "writes",
		},
		{
			name:     "policy directory does not exist",
			setup:    func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") },
			wantExit: exitPrecondition,
			wantSaid: "policy directory",
		},
		{
			name:     "policy directory is empty",
			setup:    func(t *testing.T) string { return t.TempDir() },
			wantExit: exitPrecondition,
			wantSaid: "no service",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := startDaemon(t, tc.setup(t))

			select {
			case code := <-f.exit:
				if code != tc.wantExit {
					t.Errorf("exit = %d, want %d (stderr: %q)", code, tc.wantExit, f.stderr.String())
				}
			case <-time.After(10 * time.Second):
				t.Fatal("run did not exit on an unloadable policy")
			}
			if said := f.stderr.String(); !strings.Contains(said, tc.wantSaid) {
				t.Errorf("stderr = %q, want it to mention %q", said, tc.wantSaid)
			}
			entries, err := os.ReadDir(f.socketDir)
			if err != nil {
				t.Fatalf("read socket dir: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("socket directory holds %d entries, want none: a broker that will not serve must not publish a socket", len(entries))
			}
			if f.audit.String() != "" {
				t.Errorf("audit = %q, want empty", f.audit.String())
			}
		})
	}
}

// The client group is where the capability comes from. Without it there is no
// group to hand the socket to, and a broker that started anyway would publish a
// socket nothing could open.
func TestRunRefusesToStartWithoutTheClientGroup(t *testing.T) {
	dir := t.TempDir()
	writeDocument(t, dir, "atlassian.json", policyDocument)
	stderr := &syncBuffer{}

	exit := run(context.Background(), nil, daemonConfig{
		policyDir:   dir,
		socketDir:   shortTempDir(t),
		clientGroup: "torio-mcp-clients-that-does-not-exist",
		stdout:      &syncBuffer{},
		stderr:      stderr,
	})
	if exit != exitPrecondition {
		t.Errorf("exit = %d, want %d", exit, exitPrecondition)
	}
	if msg := stderr.String(); !strings.Contains(msg, "on the host") || strings.Contains(msg, "on the guest") {
		t.Errorf("stderr = %q, want the torio mcp install remedy on the host", msg)
	}
}

func TestRunMissingPolicyRemedyInvokesHostCLI(t *testing.T) {
	stderr := &syncBuffer{}
	exit := run(context.Background(), nil, daemonConfig{
		policyDir:   filepath.Join(t.TempDir(), "absent"),
		socketDir:   shortTempDir(t),
		clientGroup: ownGroupName(t),
		stdout:      &syncBuffer{},
		stderr:      stderr,
	})
	if exit != exitPrecondition {
		t.Fatalf("exit = %d, want %d", exit, exitPrecondition)
	}
	if msg := stderr.String(); !strings.Contains(msg, "on the host") || strings.Contains(msg, "on the guest") {
		t.Errorf("stderr = %q, want the torio mcp install remedy on the host", msg)
	}
}

// The daemon takes no arguments: what it serves comes from the policy directory,
// and the paths are the guest's layout rather than the caller's. An argument is
// somebody expecting an override that does not exist, and they are told so.
func TestRunRejectsArguments(t *testing.T) {
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	exit := run(context.Background(), []string{"atlassian"}, daemonConfig{
		policyDir: t.TempDir(),
		socketDir: shortTempDir(t),
		stdout:    stdout,
		stderr:    stderr,
	})
	if exit != exitUsage {
		t.Errorf("exit = %d, want %d", exit, exitUsage)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Errorf("stderr = %q, want a usage line", stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty: stdout carries audit records only", stdout.String())
	}
}

// One socket per service, every service in the policy directory, and the grant
// stated at startup: ADR-0022 wants the granted surface legible without anybody
// reading a file, and the count of granted write tools is the number an operator
// is accountable for.
//
// The end of the test is the other half of the contract: a shutdown takes the
// sockets with it, so nothing is left for the next start to mistake for a live
// broker.
func TestRunServesEveryServiceAndCleansUpAfterItself(t *testing.T) {
	dir := t.TempDir()
	writeDocument(t, dir, "atlassian.json", policyDocument)
	writeDocument(t, dir, "slack.json",
		`{"schema_version":"1","service":"slack","upstream_endpoint":"https://slack.example.invalid/mcp","tools":[{"name":"slack_read_channel","writes":false}]}`)

	f := startDaemon(t, dir)
	atlassian := filepath.Join(f.socketDir, "atlassian.sock")
	slack := filepath.Join(f.socketDir, "slack.sock")
	waitFor(t, func() bool {
		_, a := os.Stat(atlassian)
		_, s := os.Stat(slack)
		return a == nil && s == nil
	})

	// The startup report states what each service was granted, including how many
	// of those tools write.
	wants := []string{"atlassian", "slack", "https://mcp.example.invalid/v1", "write"}
	waitFor(t, func() bool {
		said := f.stderr.String()
		for _, want := range wants {
			if !strings.Contains(said, want) {
				return false
			}
		}
		return true
	})
	said := f.stderr.String()
	for _, want := range wants {
		if !strings.Contains(said, want) {
			t.Errorf("startup report %q does not mention %q", said, want)
		}
	}

	// Enforcement is wired end to end, not only in the server type: a tool outside
	// the grant is refused by the process an operator actually starts.
	c := dialSocket(t, atlassian)
	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deleteJiraIssue","arguments":{}}}`)
	resp := c.response(t)
	if resp.Error == nil || resp.Error.Code != codeDenied {
		t.Fatalf("response = %+v, want a policy denial", resp)
	}
	if !strings.Contains(resp.Error.Message, filepath.Join(dir, "atlassian.json")) {
		t.Errorf("denial %q does not name the policy document", resp.Error.Message)
	}

	// A granted call reaches the seam where the transport will go, and says so
	// rather than pretending to have carried it.
	c.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"getJiraIssue","arguments":{}}}`)
	if resp := c.response(t); resp.Error == nil || resp.Error.Code != codeInternalError {
		t.Errorf("response = %+v, want the missing transport reported", resp)
	}
	if lines := f.auditLines(t); len(lines) != 2 {
		t.Errorf("audit holds %d lines, want 2 (deny then allow): %q", len(lines), f.audit.String())
	}

	f.cancel()
	select {
	case code := <-f.exit:
		if code != exitOK {
			t.Errorf("exit = %d, want %d (stderr: %q)", code, exitOK, f.stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after its context was cancelled")
	}
	for _, path := range []string{atlassian, slack} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived shutdown; the next start would have to tell it from a live broker", path)
		}
	}
}

// A conflict on one service's socket stops the whole daemon. Serving the rest
// would be a broker that is up and partly deaf: the agent's client for the
// missing service fails at connect, and nothing in the broker says why.
func TestRunRefusesToStartWhenASocketIsAlreadyServed(t *testing.T) {
	dir := t.TempDir()
	writeDocument(t, dir, "atlassian.json", policyDocument)
	socketDir := shortTempDir(t)

	live, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(socketDir, "atlassian.sock"), Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer live.Close()

	stderr := &syncBuffer{}
	exit := run(context.Background(), nil, daemonConfig{
		policyDir:   dir,
		socketDir:   socketDir,
		clientGroup: ownGroupName(t),
		stdout:      &syncBuffer{},
		stderr:      stderr,
	})
	if exit != exitConflict {
		t.Errorf("exit = %d, want %d (stderr: %q)", exit, exitConflict, stderr.String())
	}
}

func (f *daemonFixture) auditLines(t *testing.T) []auditLine {
	t.Helper()
	return (&testBroker{audit: f.audit}).auditLines(t)
}
