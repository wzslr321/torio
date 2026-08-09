package sshagent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
)

// Confirmer asks the operator whether one signature may proceed. A nil error is
// approval; every other outcome, including a timeout and a platform with no way
// to ask, is a denial.
//
// It is an interface so the tests in this package supply their own and none of
// them opens a window.
type Confirmer interface {
	Confirm(ctx context.Context, req SignRequest) error
}

// SignRequest is everything the operator is shown. It names the key and the
// session, and carries no signable data.
type SignRequest struct {
	Identity Identity
	Session  SessionContext
}

// Proxy is the agent Torio serves in place of the operator's own.
//
// The zero value is not usable: Serve refuses a proxy with no pinned key, no
// upstream, no confirmer or no recorder, rather than defaulting any of them to
// the permissive choice.
type Proxy struct {
	// Upstream dials the operator's real agent. It is a dial function rather
	// than a connection because every guest connection gets its own: the agent
	// protocol is strict request/response with no message ids, so two peers
	// sharing one upstream would read each other's answers.
	Upstream func() (net.Conn, error)

	// Pinned is the single identity this proxy will list and sign with. Every
	// other key in the operator's agent is invisible through it.
	Pinned Identity

	Confirm Confirmer
	Audit   Recorder
	Session SessionContext
}

func (p *Proxy) validate() error {
	switch {
	case p == nil || p.Upstream == nil:
		return errors.New("agent proxy has no upstream agent to reach")
	case len(p.Pinned.Blob) == 0:
		return errors.New("agent proxy has no pinned identity; it would sign with nothing or with anything")
	case p.Confirm == nil:
		return errors.New("agent proxy has no confirmer; a signature nobody approved is the thing it exists to prevent")
	case p.Audit == nil:
		return errors.New("agent proxy has no recorder; a decision that leaves no record is not one it may take")
	}
	return nil
}

// Serve accepts guest connections until ctx ends or the listener fails.
//
// Cancelling ctx closes the listener, so the forwarded capability ends with the
// session it was opened for rather than outliving it. Serve returns only after
// every connection it started has finished.
func (p *Proxy) Serve(ctx context.Context, l net.Listener) error {
	if err := p.validate(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-done:
		}
	}()

	var wg sync.WaitGroup
	for {
		conn, err := l.Accept()
		if err != nil {
			wg.Wait()
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			// A connection that fails is closed and forgotten. Its failure is
			// already in the audit stream if it was a decision, and if it was a
			// framing error there is nothing for the operator to act on.
			_ = p.serveConn(ctx, conn)
		}()
	}
}

func (p *Proxy) serveConn(ctx context.Context, conn net.Conn) error {
	up, err := p.Upstream()
	if err != nil {
		return err
	}
	defer up.Close()

	for {
		req, err := readFrame(conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		resp, err := p.handle(ctx, req, up)
		if err != nil {
			// The only errors that reach here are ones that must end the
			// connection: a lost audit sink, or an upstream that stopped
			// answering. Both mean the next decision could not be made
			// correctly, and a proxy that kept serving would be guessing.
			return err
		}
		if err := writeFrame(conn, resp); err != nil {
			return err
		}
	}
}

func (p *Proxy) handle(ctx context.Context, req frame, up net.Conn) (frame, error) {
	switch req.typ {
	case msgRequestIdentities:
		return p.identities(up)
	case msgSignRequest:
		return p.sign(ctx, req, up)
	default:
		// Adding a key, removing one, locking the agent, any extension: refused
		// here and never written to the operator's agent. The forwarded channel
		// exists so one key can sign; nothing else it could ask for is part of
		// that, and forwarding an unknown request to find out would be handling
		// it.
		if err := p.record(requestOther, "", false); err != nil {
			return frame{}, err
		}
		return frame{typ: msgFailure}, nil
	}
}

// identities answers with the pinned key alone.
//
// The upstream list is fetched rather than answered from p.Pinned so that a key
// unloaded from the real agent mid-session stops being offered. An empty answer
// is the honest result in that case: the guest is told there is no key, which is
// true, instead of being told there is one that will then refuse to sign.
func (p *Proxy) identities(up net.Conn) (frame, error) {
	answer, err := roundTrip(up, frame{typ: msgRequestIdentities})
	if err != nil {
		return frame{}, err
	}
	if answer.typ != msgIdentitiesAnswer {
		if err := p.record(requestIdentities, p.Pinned.Fingerprint(), false); err != nil {
			return frame{}, err
		}
		return frame{typ: msgFailure}, nil
	}
	ids, err := parseIdentities(answer.body)
	if err != nil {
		return frame{}, err
	}
	var kept []Identity
	for _, id := range ids {
		if bytes.Equal(id.Blob, p.Pinned.Blob) {
			kept = append(kept, id)
		}
	}
	if err := p.record(requestIdentities, p.Pinned.Fingerprint(), len(kept) == 1); err != nil {
		return frame{}, err
	}
	return frame{typ: msgIdentitiesAnswer, body: encodeIdentities(kept)}, nil
}

func (p *Proxy) sign(ctx context.Context, req frame, up net.Conn) (frame, error) {
	blob, err := signRequestKey(req.body)
	if err != nil {
		return frame{}, err
	}
	if !bytes.Equal(blob, p.Pinned.Blob) {
		// The fingerprint of the key that was asked for is recorded, not the key
		// the proxy holds: which other key a guest reached for is the whole
		// value of the line.
		if err := p.record(requestSign, Identity{Blob: blob}.Fingerprint(), false); err != nil {
			return frame{}, err
		}
		return frame{typ: msgFailure}, nil
	}
	if err := p.Confirm.Confirm(ctx, SignRequest{Identity: p.Pinned, Session: p.Session}); err != nil {
		if err := p.record(requestSign, p.Pinned.Fingerprint(), false); err != nil {
			return frame{}, err
		}
		return frame{typ: msgFailure}, nil
	}
	// Recorded before the signature is obtained, never after. A line written
	// afterwards is the line that goes missing exactly when it matters most: if
	// the process dies between the two, the signature happened and nothing says
	// so.
	if err := p.record(requestSign, p.Pinned.Fingerprint(), true); err != nil {
		return frame{}, err
	}
	return roundTrip(up, req)
}

func (p *Proxy) record(request, fingerprint string, allowed bool) error {
	if err := p.Audit.Record(newDecision(request, fingerprint, allowed)); err != nil {
		// Fail closed, the rule ADR-0012 already set for the MCP broker: a
		// decision that could not be recorded is not one this proxy may take.
		return errors.New("agent decision refused because its audit record could not be written")
	}
	return nil
}

func roundTrip(up net.Conn, req frame) (frame, error) {
	if err := writeFrame(up, req); err != nil {
		return frame{}, err
	}
	return readFrame(up)
}
