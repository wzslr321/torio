package sshagent

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAgent stands in for the operator's real ssh-agent. It records the message
// types it was asked for, which is how the tests below prove that a refusal
// never reached the operator's keys.
type fakeAgent struct {
	mu         sync.Mutex
	identities []Identity
	signature  []byte
	seen       []byte
}

func (f *fakeAgent) serve(conn net.Conn) {
	defer conn.Close()
	for {
		req, err := readFrame(conn)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.seen = append(f.seen, req.typ)
		identities := f.identities
		signature := f.signature
		f.mu.Unlock()

		var resp frame
		switch req.typ {
		case msgRequestIdentities:
			resp = frame{typ: msgIdentitiesAnswer, body: encodeIdentities(identities)}
		case msgSignRequest:
			resp = frame{typ: msgSignResponse, body: appendString(nil, signature)}
		default:
			resp = frame{typ: msgFailure}
		}
		if err := writeFrame(conn, resp); err != nil {
			return
		}
	}
}

func (f *fakeAgent) requests() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return bytes.Clone(f.seen)
}

func (f *fakeAgent) dial() (net.Conn, error) {
	client, server := net.Pipe()
	go f.serve(server)
	return client, nil
}

type confirmerFunc func(context.Context, SignRequest) error

func (c confirmerFunc) Confirm(ctx context.Context, req SignRequest) error {
	return c(ctx, req)
}

func allowAll() (Confirmer, *int) {
	asked := 0
	return confirmerFunc(func(context.Context, SignRequest) error {
		asked++
		return nil
	}), &asked
}

func denyAll() (Confirmer, *int) {
	asked := 0
	return confirmerFunc(func(context.Context, SignRequest) error {
		asked++
		return errDenied
	}), &asked
}

type fakeRecorder struct {
	mu        sync.Mutex
	decisions []Decision
	err       error
}

func (r *fakeRecorder) Record(d Decision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.decisions = append(r.decisions, d)
	return nil
}

func (r *fakeRecorder) all() []Decision {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Decision(nil), r.decisions...)
}

// testProxy wires a proxy over a fake agent holding the pinned key plus one the
// guest must never see.
func testProxy(t *testing.T, confirm Confirmer) (*Proxy, *fakeAgent, *fakeRecorder) {
	t.Helper()
	pinned := goldenIdentity(t)
	agent := &fakeAgent{
		identities: []Identity{
			pinned,
			{Blob: []byte("the-other-key-in-the-keyring"), Comment: "personal"},
		},
		signature: []byte("signature-bytes"),
	}
	rec := &fakeRecorder{}
	return &Proxy{
		Upstream: agent.dial,
		Pinned:   pinned,
		Confirm:  confirm,
		Audit:    rec,
		Session:  SessionContext{ProjectID: "torio-cc", Remote: "github.com/wzslr321/torio", Branch: "main", Ahead: 3},
	}, agent, rec
}

// guestConn runs one connection of p and hands back the end a guest would hold.
func guestConn(t *testing.T, p *Proxy) net.Conn {
	t.Helper()
	guest, proxySide := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		// Serve closes the connection when serveConn returns; the helper has to
		// do the same, or a test that expects the proxy to hang up sits on its
		// read deadline instead of seeing the hangup.
		defer proxySide.Close()
		done <- p.serveConn(ctx, proxySide)
	}()
	t.Cleanup(func() {
		cancel()
		_ = guest.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("serveConn did not return after the guest hung up")
		}
	})
	if err := guest.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	return guest
}

func ask(t *testing.T, conn net.Conn, req frame) frame {
	t.Helper()
	if err := writeFrame(conn, req); err != nil {
		t.Fatalf("writeFrame() error = %v", err)
	}
	resp, err := readFrame(conn)
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	return resp
}

func signRequestFor(blob []byte) frame {
	body := appendString(nil, blob)
	body = appendString(body, []byte("data-under-signature"))
	return frame{typ: msgSignRequest, body: body}
}

// TestIdentitiesShowOnlyThePinnedKey is the whole difference from `ssh -A`.
// Today a session sees the entire keyring; through the proxy it sees one key.
func TestIdentitiesShowOnlyThePinnedKey(t *testing.T) {
	confirm, _ := allowAll()
	p, agent, rec := testProxy(t, confirm)
	conn := guestConn(t, p)

	resp := ask(t, conn, frame{typ: msgRequestIdentities})
	if resp.typ != msgIdentitiesAnswer {
		t.Fatalf("response type = %d, want SSH_AGENT_IDENTITIES_ANSWER", resp.typ)
	}
	ids, err := parseIdentities(resp.body)
	if err != nil {
		t.Fatalf("parseIdentities() error = %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("guest was offered %d identities, want exactly the pinned one", len(ids))
	}
	if ids[0].Fingerprint() != goldenFingerprint {
		t.Errorf("guest was offered %s, want %s", ids[0].Fingerprint(), goldenFingerprint)
	}
	if len(agent.identities) != 2 {
		t.Fatal("the fake agent should still hold both keys; the test proves filtering, not unloading")
	}
	if got := rec.all(); len(got) != 1 || got[0].Request != requestIdentities || !got[0].Allowed {
		t.Errorf("audit = %+v, want one allowed identities decision", got)
	}
}

// TestIdentitiesAreEmptyOnceThePinnedKeyIsUnloaded proves the list is fetched
// rather than answered from memory. A guest told a key exists that will then
// refuse to sign would be debugging Torio instead of reading the dialog.
func TestIdentitiesAreEmptyOnceThePinnedKeyIsUnloaded(t *testing.T) {
	confirm, _ := allowAll()
	p, agent, rec := testProxy(t, confirm)
	agent.identities = []Identity{{Blob: []byte("the-other-key-in-the-keyring"), Comment: "personal"}}
	conn := guestConn(t, p)

	resp := ask(t, conn, frame{typ: msgRequestIdentities})
	ids, err := parseIdentities(resp.body)
	if err != nil {
		t.Fatalf("parseIdentities() error = %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("guest was offered %d identities after the pinned key was unloaded, want none", len(ids))
	}
	if got := rec.all(); len(got) != 1 || got[0].Allowed {
		t.Errorf("audit = %+v, want one refused identities decision", got)
	}
}

func TestSignIsForwardedOnceTheOperatorApproves(t *testing.T) {
	confirm, asked := allowAll()
	p, agent, rec := testProxy(t, confirm)
	conn := guestConn(t, p)

	resp := ask(t, conn, signRequestFor(goldenIdentity(t).Blob))
	if resp.typ != msgSignResponse {
		t.Fatalf("response type = %d, want SSH_AGENT_SIGN_RESPONSE", resp.typ)
	}
	if *asked != 1 {
		t.Errorf("operator was asked %d times, want once per signature", *asked)
	}
	if !bytes.Contains(resp.body, []byte("signature-bytes")) {
		t.Error("the upstream signature did not reach the guest")
	}
	if !bytes.Contains(agent.requests(), []byte{msgSignRequest}) {
		t.Error("the sign request never reached the operator's agent")
	}
	if got := rec.all(); len(got) != 1 || got[0].Request != requestSign || !got[0].Allowed {
		t.Errorf("audit = %+v, want one allowed sign decision", got)
	}
	if got := rec.all(); got[0].Fingerprint != goldenFingerprint {
		t.Errorf("audit fingerprint = %q, want %q", got[0].Fingerprint, goldenFingerprint)
	}
}

// TestSignIsRefusedWhenTheOperatorDeclines proves the denial is real and not
// cosmetic: the operator's agent is never asked to sign at all.
func TestSignIsRefusedWhenTheOperatorDeclines(t *testing.T) {
	confirm, asked := denyAll()
	p, agent, rec := testProxy(t, confirm)
	conn := guestConn(t, p)

	resp := ask(t, conn, signRequestFor(goldenIdentity(t).Blob))
	if resp.typ != msgFailure {
		t.Fatalf("response type = %d, want SSH_AGENT_FAILURE", resp.typ)
	}
	if *asked != 1 {
		t.Errorf("operator was asked %d times, want once", *asked)
	}
	if bytes.Contains(agent.requests(), []byte{msgSignRequest}) {
		t.Error("a declined signature still reached the operator's agent")
	}
	if got := rec.all(); len(got) != 1 || got[0].Allowed {
		t.Errorf("audit = %+v, want one refused sign decision", got)
	}
}

// TestSignForAnotherKeyIsRefusedWithoutAsking proves the pin is enforced before
// the operator is involved. A dialog for a key the guest may not use would train
// the operator to click through the ones that matter.
func TestSignForAnotherKeyIsRefusedWithoutAsking(t *testing.T) {
	confirm, asked := allowAll()
	p, agent, rec := testProxy(t, confirm)
	conn := guestConn(t, p)

	other := []byte("the-other-key-in-the-keyring")
	resp := ask(t, conn, signRequestFor(other))
	if resp.typ != msgFailure {
		t.Fatalf("response type = %d, want SSH_AGENT_FAILURE", resp.typ)
	}
	if *asked != 0 {
		t.Errorf("operator was asked %d times about an unpinned key, want never", *asked)
	}
	if bytes.Contains(agent.requests(), []byte{msgSignRequest}) {
		t.Error("a request for an unpinned key reached the operator's agent")
	}
	got := rec.all()
	if len(got) != 1 || got[0].Allowed {
		t.Fatalf("audit = %+v, want one refused sign decision", got)
	}
	if want := (Identity{Blob: other}).Fingerprint(); got[0].Fingerprint != want {
		t.Errorf("audit fingerprint = %q, want the key that was reached for (%q)", got[0].Fingerprint, want)
	}
}

// TestUnsupportedRequestsNeverReachTheAgent covers adding a key, removing one,
// locking the agent and every extension, by number. The forwarded channel exists
// so one key can sign; forwarding an unknown request to see what happens would
// be handling it.
func TestUnsupportedRequestsNeverReachTheAgent(t *testing.T) {
	// SSH_AGENTC_ADD_IDENTITY, REMOVE_IDENTITY, REMOVE_ALL_IDENTITIES,
	// LOCK, UNLOCK, ADD_SMARTCARD_KEY, EXTENSION.
	for _, typ := range []byte{17, 18, 19, 22, 23, 20, 27} {
		confirm, asked := allowAll()
		p, agent, rec := testProxy(t, confirm)
		conn := guestConn(t, p)

		resp := ask(t, conn, frame{typ: typ})
		if resp.typ != msgFailure {
			t.Errorf("message %d: response type = %d, want SSH_AGENT_FAILURE", typ, resp.typ)
		}
		if len(agent.requests()) != 0 {
			t.Errorf("message %d reached the operator's agent", typ)
		}
		if *asked != 0 {
			t.Errorf("message %d put a dialog in front of the operator", typ)
		}
		if got := rec.all(); len(got) != 1 || got[0].Request != requestOther || got[0].Allowed {
			t.Errorf("message %d: audit = %+v, want one refused unsupported decision", typ, got)
		}
	}
}

// TestAnUnrecordableDecisionEndsTheConnection is the fail-closed rule ADR-0012
// set for the MCP broker, applied here: a decision that leaves no record is not
// one this proxy may take.
func TestAnUnrecordableDecisionEndsTheConnection(t *testing.T) {
	confirm, _ := allowAll()
	p, agent, rec := testProxy(t, confirm)
	rec.err = errors.New("audit sink is unavailable")
	conn := guestConn(t, p)

	if err := writeFrame(conn, signRequestFor(goldenIdentity(t).Blob)); err != nil {
		t.Fatalf("writeFrame() error = %v", err)
	}
	if _, err := readFrame(conn); err == nil {
		t.Error("the proxy answered a request it could not record")
	}
	if bytes.Contains(agent.requests(), []byte{msgSignRequest}) {
		t.Error("an unrecordable signature still reached the operator's agent")
	}
}

func TestSignRequestCarriesTheSessionToTheOperator(t *testing.T) {
	var seen SignRequest
	p, _, _ := testProxy(t, confirmerFunc(func(_ context.Context, req SignRequest) error {
		seen = req
		return nil
	}))
	conn := guestConn(t, p)
	ask(t, conn, signRequestFor(goldenIdentity(t).Blob))

	if seen.Session.ProjectID != "torio-cc" || seen.Session.Ahead != 3 {
		t.Errorf("confirmer saw session %+v, want the proxy's own", seen.Session)
	}
	if seen.Identity.Fingerprint() != goldenFingerprint {
		t.Errorf("confirmer saw key %s, want %s", seen.Identity.Fingerprint(), goldenFingerprint)
	}
}

// TestServeRefusesAnIncompleteProxy proves none of the four fields defaults to
// the permissive choice. A proxy with no confirmer that signed anyway would be
// weaker than the `ssh -A` it replaces.
func TestServeRefusesAnIncompleteProxy(t *testing.T) {
	pinned := goldenIdentity(t)
	agent := &fakeAgent{identities: []Identity{pinned}}
	confirm, _ := allowAll()
	complete := Proxy{Upstream: agent.dial, Pinned: pinned, Confirm: confirm, Audit: &fakeRecorder{}}

	for name, mutate := range map[string]func(*Proxy){
		"no upstream":   func(p *Proxy) { p.Upstream = nil },
		"no pinned key": func(p *Proxy) { p.Pinned = Identity{} },
		"no confirmer":  func(p *Proxy) { p.Confirm = nil },
		"no recorder":   func(p *Proxy) { p.Audit = nil },
	} {
		p := complete
		mutate(&p)
		listener := unixListener(t)
		if err := p.Serve(t.Context(), listener); err == nil {
			t.Errorf("%s: Serve() accepted an incomplete proxy", name)
		}
		_ = listener.Close()
	}
}

// TestServeEndsWithItsContext proves the capability dies with the session rather
// than outliving it, which is the property `ssh -A` already had and this must
// not lose.
func TestServeEndsWithItsContext(t *testing.T) {
	confirm, _ := allowAll()
	p, _, _ := testProxy(t, confirm)
	listener := unixListener(t)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- p.Serve(ctx, listener) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve() error = %v, want nil after cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}

	if _, err := net.Dial("unix", listener.Addr().String()); err == nil {
		t.Error("the socket still accepts connections after the session ended")
	}
}

// TestServeOverARealSocket exercises the whole path a guest takes: connect to
// the Unix socket, list, sign.
func TestServeOverARealSocket(t *testing.T) {
	confirm, _ := allowAll()
	p, _, _ := testProxy(t, confirm)
	listener := unixListener(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = p.Serve(ctx, listener) }()

	conn, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial the proxy socket: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	resp := ask(t, conn, frame{typ: msgRequestIdentities})
	ids, err := parseIdentities(resp.body)
	if err != nil || len(ids) != 1 {
		t.Fatalf("identities over a real socket = %v (%d), want the pinned key", err, len(ids))
	}
	if resp := ask(t, conn, signRequestFor(ids[0].Blob)); resp.typ != msgSignResponse {
		t.Errorf("sign over a real socket = %d, want SSH_AGENT_SIGN_RESPONSE", resp.typ)
	}
}

func unixListener(t *testing.T) net.Listener {
	t.Helper()
	socket, err := Listen(shortTempRoot(t))
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	return socket.Listener
}

func TestPromptMessageNamesTheSessionAndItsLimits(t *testing.T) {
	msg := promptMessage(SignRequest{
		Identity: goldenIdentity(t),
		Session:  SessionContext{ProjectID: "torio-cc", Remote: "github.com/wzslr321/torio", Branch: "main", Ahead: 3},
	})
	for _, want := range []string{"torio-cc", "github.com/wzslr321/torio", "main", "3 commits ahead", goldenFingerprint} {
		if !strings.Contains(msg, want) {
			t.Errorf("prompt does not mention %q:\n%s", want, msg)
		}
	}
	// The disclaimer is the load-bearing half. A sign request carries no Git
	// context, so a prompt that only listed the facts above would be read as a
	// claim that Torio had seen the push.
	if !strings.Contains(msg, "cannot see what the key will be used for") {
		t.Errorf("prompt does not say what Torio cannot see:\n%s", msg)
	}
}
