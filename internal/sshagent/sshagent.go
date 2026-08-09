// Package sshagent is the agent Torio forwards in place of the operator's own.
//
// `ssh -A` forwards whatever SSH_AUTH_SOCK names, and what it forwards is the
// whole agent: every loaded identity, every signature it will ever make, for the
// length of the session. This package interposes on that. It answers a key list
// with one pinned identity, gates every signature on an explicit confirmation on
// the host, refuses every other request in the protocol without passing it on,
// and records each decision before the decision takes effect.
//
// It is a control against a compromised guest, not a compromised host: it runs
// as the operator, on the operator's machine, with the operator's real agent one
// dial away. What it removes is a guest's ability to reach a keyring it was
// never meant to touch, and its ability to sign without the operator present
// (ADR-0015).
//
// No private key material passes through this package and no type here has a
// field that could hold any. The data a signature is made over is parsed past
// and never retained: this package decides whether a key may be used, not what
// it is used on.
package sshagent

import (
	"crypto/sha256"
	"encoding/base64"
)

// Identity is one public key as an agent reports it: the wire blob and the
// comment the operator gave it.
type Identity struct {
	Blob    []byte
	Comment string
}

// Fingerprint is the SHA256 form `ssh-add -l` prints, so an operator pins a key
// by pasting what is already in front of them rather than by learning a second
// notation for the same key.
func (i Identity) Fingerprint() string {
	sum := sha256.Sum256(i.Blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// SessionContext is what the operator is shown when a signature is requested.
//
// It is gathered once, by a read-only probe of the checkout when the session
// opens. A sign request carries nothing about Git — it is a key and a blob — so
// this describes what the checkout was at that moment and never claims to
// describe what is being pushed. Torio's existing refusal to say what a session
// pushed is unchanged by this package.
type SessionContext struct {
	ProjectID string
	Remote    string
	Branch    string
	Ahead     int
}
