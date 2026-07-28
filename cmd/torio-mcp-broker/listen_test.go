package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

// The socket is the boundary. ADR-0022 §3 puts it at 0660
// torio-mcp:torio-mcp-clients, and the mode is set explicitly rather than left to
// umask: umask is inherited from whatever started the process, so a broker that
// relied on it would publish a world-connectable socket the day somebody's unit
// file, login shell or init system disagreed — and nothing would look wrong.
func TestListenSetsTheSocketModeExplicitly(t *testing.T) {
	dir := shortTempDir(t)
	// A permissive umask stands in for that disagreement: with it set, a socket
	// left to inherit its mode is reachable by everyone on the guest.
	defer syscall.Umask(syscall.Umask(0))

	ln, err := listenService(dir, testService, -1)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	info, err := os.Stat(ln.Addr().String())
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != socketMode {
		t.Errorf("socket mode = %04o, want %04o", perm, socketMode)
	}
}

// The socket's group is the whole grant: membership in it is the only privilege
// the agent identity is given (ADR-0022 §3). Handing the socket to that group is
// therefore not a detail of installation — it is the moment the capability is
// conferred, so it is done here and proved here.
func TestListenHandsTheSocketToTheClientGroup(t *testing.T) {
	dir := shortTempDir(t)
	gid := groupOtherThanDefault(t, dir)

	ln, err := listenService(dir, testService, gid)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	if got := socketGID(t, ln.Addr().String()); got != gid {
		t.Errorf("socket gid = %d, want %d", got, gid)
	}
}

// A group the process cannot hand the socket to is a boundary that did not get
// built. Serving anyway would publish a socket only the broker itself can open,
// which reads as a healthy broker and behaves as a broken one.
func TestListenRefusesWhenTheClientGroupCannotBeSet(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can chown to any group, so the refusal under test cannot happen")
	}
	dir := shortTempDir(t)

	// A gid the test process is certainly not a member of. chown to it must fail,
	// and the failure must not leave a socket behind.
	const foreignGID = 0x7ffffffe
	_, err := listenService(dir, testService, foreignGID)
	if err == nil {
		t.Fatal("listen succeeded with an unusable client group")
	}
	if !strings.Contains(err.Error(), "group") {
		t.Errorf("error = %q, want it to name the group as the problem", err)
	}
	if _, err := os.Stat(filepath.Join(dir, testService+socketSuffix)); !os.IsNotExist(err) {
		t.Error("a socket was left behind after the boundary failed to be built")
	}
}

// A crashed broker leaves its socket file on disk. That file passes every test of
// owner, group and mode and refuses every connection, so the broker has to be
// able to take the address back — otherwise one crash means a guest that never
// serves MCP again until somebody deletes a file by hand.
func TestListenReplacesAStaleSocket(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, testService+socketSuffix)

	dead, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Leaving the file behind is the point: this is what a crash leaves.
	dead.SetUnlinkOnClose(false)
	if err := dead.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ln, err := listenService(dir, testService, -1)
	if err != nil {
		t.Fatalf("listen over a stale socket: %v", err)
	}
	defer ln.Close()

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("the replaced socket does not accept connections: %v", err)
	}
	conn.Close()
}

// A live broker is not stale. Unlinking a socket somebody is serving on would
// leave that broker running and unreachable — its clients would connect to the
// newcomer while the old one held the credentials and the policy it started with.
func TestListenRefusesWhenAnotherBrokerIsListening(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, testService+socketSuffix)

	live, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer live.Close()

	_, err = listenService(dir, testService, -1)
	if err == nil {
		t.Fatal("listen succeeded while another broker was serving the same service")
	}
	if !strings.Contains(err.Error(), "already") {
		t.Errorf("error = %q, want it to say the address is already served", err)
	}

	// The live socket must still be there and still answering.
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("the live broker's socket was disturbed: %v", err)
	}
	conn.Close()
}

// Anything at the socket path that is not a socket is refused and left alone.
// The broker removes one specific thing — a socket nothing is listening on — and
// a rule that removed whatever was in the way would be a delete primitive aimed
// at a path in /run.
func TestListenRefusesAPathThatIsNotASocket(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, testService+socketSuffix)
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := listenService(dir, testService, -1); err == nil {
		t.Fatal("listen succeeded over a regular file")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the file at the socket path was removed: %v", err)
	}
}

// The service name rule belongs to internal/mcpbroker, and this binary must use
// it rather than a second copy: the relay resolves the same path from the same
// rule, and a name one side accepts and the other rejects is a socket nothing can
// reach.
func TestListenRejectsANonSlugService(t *testing.T) {
	dir := shortTempDir(t)
	for _, service := range []string{"", "../../etc/shadow", "Atlassian", "atlassian.sock"} {
		if _, err := listenService(dir, service, -1); err == nil {
			t.Errorf("listenService(%q) succeeded, want rejection", service)
		}
	}
}

// The longest name the rule accepts must still produce an address the kernel can
// hold. If either the directory or the bound grows, this fails here rather than
// as an EINVAL on a guest somebody is debugging.
func TestSocketPathFitsSunPath(t *testing.T) {
	const sunPathLimit = 104
	longest := filepath.Join(socketDir, strings.Repeat("a", mcpbroker.MaxServiceNameLen)+socketSuffix)
	if len(longest)+1 > sunPathLimit { // +1 for the NUL the kernel stores
		t.Errorf("longest socket path is %d bytes, over the %d-byte sun_path limit: %s",
			len(longest)+1, sunPathLimit, longest)
	}
}

func socketGID(t *testing.T, path string) int {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat of %s carries no unix ownership", path)
	}
	return int(stat.Gid)
}

// groupOtherThanDefault returns a group the test process belongs to that a socket
// in dir would not already have. Without it the chown could be a no-op and the
// test would pass on a broker that never made the call.
func groupOtherThanDefault(t *testing.T, dir string) int {
	t.Helper()
	probe := filepath.Join(dir, "probe.sock")
	ln, err := net.Listen("unix", probe)
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	defer os.Remove(probe)
	defer ln.Close()

	inherited := socketGID(t, probe)
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("groups: %v", err)
	}
	for _, gid := range groups {
		if gid != inherited {
			return gid
		}
	}
	t.Skip("this user belongs to no second group, so a chown cannot be told from a no-op")
	return -1
}
