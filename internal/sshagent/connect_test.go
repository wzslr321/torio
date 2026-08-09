package sshagent

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"
)

func agentHolding(ids ...Identity) func() (net.Conn, error) {
	return (&fakeAgent{identities: ids}).dial
}

// TestPinIdentityTakesASolitaryKey: an agent with one key has nothing to narrow,
// so requiring the operator to name it would be ceremony rather than a control.
func TestPinIdentityTakesASolitaryKey(t *testing.T) {
	only := goldenIdentity(t)
	got, err := PinIdentity(agentHolding(only), "")
	if err != nil {
		t.Fatalf("PinIdentity() error = %v", err)
	}
	if got.Fingerprint() != goldenFingerprint {
		t.Errorf("PinIdentity() = %s, want %s", got.Fingerprint(), goldenFingerprint)
	}
}

// TestPinIdentityRefusesToChooseAmongSeveral proves Torio never picks which key
// a guest may use. The remedy is in the error, as a fingerprint the operator can
// paste straight into their config.
func TestPinIdentityRefusesToChooseAmongSeveral(t *testing.T) {
	_, err := PinIdentity(agentHolding(goldenIdentity(t), Identity{Blob: []byte("second"), Comment: "personal"}), "")
	if err == nil {
		t.Fatal("PinIdentity() chose a key on the operator's behalf")
	}
	if !strings.Contains(err.Error(), "operator_key") || !strings.Contains(err.Error(), goldenFingerprint) {
		t.Errorf("PinIdentity() error = %v, want the remedy and the fingerprints", err)
	}
}

func TestPinIdentityMatchesAFingerprintOrAComment(t *testing.T) {
	pinned := goldenIdentity(t)
	other := Identity{Blob: []byte("second"), Comment: "personal"}

	for name, want := range map[string]string{
		"fingerprint": goldenFingerprint,
		"comment":     "torio-test-key",
	} {
		got, err := PinIdentity(agentHolding(pinned, other), want)
		if err != nil {
			t.Fatalf("%s: PinIdentity() error = %v", name, err)
		}
		if got.Fingerprint() != goldenFingerprint {
			t.Errorf("%s: PinIdentity() = %s, want %s", name, got.Fingerprint(), goldenFingerprint)
		}
	}
}

// TestPinIdentityRefusesAnAmbiguousComment: a comment that names two keys names
// neither, and the error says so by asking for the notation that cannot repeat.
func TestPinIdentityRefusesAnAmbiguousComment(t *testing.T) {
	first := Identity{Blob: []byte("first"), Comment: "work"}
	second := Identity{Blob: []byte("second"), Comment: "work"}

	_, err := PinIdentity(agentHolding(first, second), "work")
	if err == nil {
		t.Fatal("PinIdentity() accepted a comment naming two keys")
	}
	if !strings.Contains(err.Error(), "pin a fingerprint instead") {
		t.Errorf("PinIdentity() error = %v, want the fingerprint remedy", err)
	}
}

func TestPinIdentityReportsAnUnmatchedPin(t *testing.T) {
	_, err := PinIdentity(agentHolding(goldenIdentity(t)), "SHA256:not-a-key-this-agent-holds")
	if err == nil {
		t.Fatal("PinIdentity() accepted a pin no identity matches")
	}
	if !strings.Contains(err.Error(), goldenFingerprint) {
		t.Errorf("PinIdentity() error = %v, want the identities it does hold", err)
	}
}

func TestPinIdentityReportsAnEmptyAgent(t *testing.T) {
	_, err := PinIdentity(agentHolding(), "")
	if err == nil || !strings.Contains(err.Error(), "ssh-add") {
		t.Errorf("PinIdentity() error = %v, want the empty-agent remedy", err)
	}
}

// TestPinIdentityDropsTheDialCause keeps the projects preflight's rule: this is
// the one diagnostic derived from talking to an agent, and agent traffic is
// where key material would be if it were anywhere.
func TestPinIdentityDropsTheDialCause(t *testing.T) {
	dial := func() (net.Conn, error) {
		return nil, errors.New("dial unix /private/tmp/com.apple.launchd.XXXX/Listeners: connect: no such file")
	}
	_, err := PinIdentity(dial, "")
	if err == nil {
		t.Fatal("PinIdentity() succeeded against an agent it could not reach")
	}
	if strings.Contains(err.Error(), "launchd") {
		t.Errorf("PinIdentity() error = %v, want the cause dropped", err)
	}
	if !strings.Contains(err.Error(), "could not be queried") {
		t.Errorf("PinIdentity() error = %v, want the unqueryable-agent message", err)
	}
}

// TestListenIsPrivateAndTemporary proves the directory mode is the access
// control. Socket modes are not honoured uniformly across platforms; a
// directory's are.
func TestListenIsPrivateAndTemporary(t *testing.T) {
	root := t.TempDir()
	socket, err := Listen(root)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	dir := socket.Path[:strings.LastIndex(socket.Path, "/")]
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat the socket directory: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("socket directory mode = %04o, want 0700", perm)
	}
	if _, err := os.Stat(socket.Path); err != nil {
		t.Errorf("socket was not created: %v", err)
	}

	if err := socket.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the socket directory outlived the session: %v", err)
	}
}

// TestListenGivesEachSessionItsOwnPath proves a path never names two
// capabilities, so a stale socket from a crashed session is not rebound.
func TestListenGivesEachSessionItsOwnPath(t *testing.T) {
	root := t.TempDir()
	first, err := Listen(root)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer first.Close()
	second, err := Listen(root)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer second.Close()

	if first.Path == second.Path {
		t.Errorf("two sessions share the socket path %s", first.Path)
	}
}

func TestUpstreamFromEnvRefusesAnUnsetAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	if _, err := UpstreamFromEnv(); err == nil || !strings.Contains(err.Error(), "SSH_AUTH_SOCK") {
		t.Errorf("UpstreamFromEnv() error = %v, want the unset-agent refusal", err)
	}
}
