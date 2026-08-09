package sshagent

import (
	"encoding/binary"
	"errors"
	"io"
)

// Agent protocol message numbers (draft-miller-ssh-agent). Only the numbers this
// package acts on are named — and refusing a message is acting on it, which is
// why SSH_AGENTC_EXTENSION is here.
//
// It was left unnamed at first, on the argument that a constant for a message we
// refuse is the first step toward handling it. Use disproved that. OpenSSH 8.9
// and later probe a forwarded agent with `session-bind@openssh.com` on every
// connection, so every ordinary `git push` put a refusal in the decision log
// next to the ones that mean something. Two benign `allowed:false` lines per
// connection is how an operator learns to skim past the line that matters.
const (
	msgFailure           = 5
	msgRequestIdentities = 11
	msgIdentitiesAnswer  = 12
	msgSignRequest       = 13
	msgExtension         = 27
)

// maxFrame bounds one protocol message. It is OpenSSH's own AGENT_MAX_LEN: a
// peer asking for more is not speaking the protocol, and a proxy that grew its
// buffer to match would let a guest exhaust host memory through a socket the
// operator handed it on purpose.
const maxFrame = 256 * 1024

var errTruncated = errors.New("agent message body is truncated")

// frame is one agent message: the type byte and the body after it. The length
// prefix is not kept, because it is a property of the encoding and not of the
// message.
type frame struct {
	typ  byte
	body []byte
}

// readFrame reads exactly one message. A short read is an error rather than a
// partial frame: the protocol is strictly request/response with no message ids,
// so a stream that has lost its framing cannot be resynchronized and must end.
func readFrame(r io.Reader) (frame, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return frame{}, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 {
		return frame{}, errors.New("agent frame carries no message type")
	}
	if length > maxFrame {
		return frame{}, errors.New("agent frame exceeds the protocol maximum")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return frame{}, err
	}
	return frame{typ: payload[0], body: payload[1:]}, nil
}

func writeFrame(w io.Writer, f frame) error {
	length := len(f.body) + 1
	if length > maxFrame {
		return errors.New("agent frame exceeds the protocol maximum")
	}
	out := make([]byte, 4+length)
	binary.BigEndian.PutUint32(out[:4], uint32(length))
	out[4] = f.typ
	copy(out[5:], f.body)
	_, err := w.Write(out)
	return err
}

// bodyReader walks a message body without ever indexing past it. Every accessor
// reports a short body as an error instead of returning a zero value: a
// truncated identity list and an empty one are different facts, and only one of
// them means the agent holds no key.
type bodyReader struct{ b []byte }

func (r *bodyReader) uint32() (uint32, error) {
	if len(r.b) < 4 {
		return 0, errTruncated
	}
	v := binary.BigEndian.Uint32(r.b[:4])
	r.b = r.b[4:]
	return v, nil
}

func (r *bodyReader) string() ([]byte, error) {
	n, err := r.uint32()
	if err != nil {
		return nil, err
	}
	if uint64(n) > uint64(len(r.b)) {
		return nil, errTruncated
	}
	s := r.b[:n]
	r.b = r.b[n:]
	return s, nil
}

// parseIdentities decodes an SSH_AGENT_IDENTITIES_ANSWER body.
func parseIdentities(body []byte) ([]Identity, error) {
	r := &bodyReader{b: body}
	count, err := r.uint32()
	if err != nil {
		return nil, err
	}
	// A declared count is not an allocation budget. Each identity costs at least
	// two length prefixes, so a count past that bound is a malformed message and
	// not a large one.
	if uint64(count)*8 > uint64(len(body)) {
		return nil, errors.New("agent identity count exceeds the message")
	}
	ids := make([]Identity, 0, count)
	for range count {
		blob, err := r.string()
		if err != nil {
			return nil, err
		}
		comment, err := r.string()
		if err != nil {
			return nil, err
		}
		ids = append(ids, Identity{Blob: blob, Comment: string(comment)})
	}
	return ids, nil
}

func encodeIdentities(ids []Identity) []byte {
	body := binary.BigEndian.AppendUint32(nil, uint32(len(ids)))
	for _, id := range ids {
		body = appendString(body, id.Blob)
		body = appendString(body, []byte(id.Comment))
	}
	return body
}

func appendString(dst, s []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(s)))
	return append(dst, s...)
}

// signRequestKey returns only the public key a sign request names.
//
// The data to be signed follows it in the same message and is deliberately not
// returned. This package decides whether a key may be used; reading what it
// would be used on would put a Git protocol exchange, and whatever a caller had
// put in it, inside a host process that has no reason to hold it.
func signRequestKey(body []byte) ([]byte, error) {
	r := &bodyReader{b: body}
	return r.string()
}
