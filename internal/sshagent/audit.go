package sshagent

import (
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// The request kinds a decision can be about. The vocabulary is closed, and it
// distinguishes exactly one refusal from the rest.
//
// An extension is what OpenSSH sends unprompted, on every connection, to see
// whether the agent supports session binding. A request to add, remove or lock a
// key is something a guest went out of its way to ask for. Recording both as
// "unsupported" made the second invisible inside the noise of the first, which
// is the opposite of what a decision log is for.
const (
	requestIdentities = "identities"
	requestSign       = "sign"
	requestExtension  = "extension"
	requestOther      = "unsupported"
)

// Decision is one audited outcome. It has no field capable of holding a
// signature, the data under it, a comment, or anything else a guest supplied:
// a key is named by fingerprint, which the operator can already read from
// `ssh-add -l`, and nothing else crosses into the record.
type Decision struct {
	Time        time.Time `json:"time"`
	Request     string    `json:"request"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Allowed     bool      `json:"allowed"`
}

func newDecision(request, fingerprint string, allowed bool) Decision {
	return Decision{
		Time:        time.Now().UTC(),
		Request:     request,
		Fingerprint: fingerprint,
		Allowed:     allowed,
	}
}

// Recorder is the durable decision log. A decision that cannot be recorded is a
// denial.
type Recorder interface {
	Record(Decision) error
}

// SyncWriter is the append-only sink a durable decision needs. *os.File
// implements it.
type SyncWriter interface {
	io.Writer
	Sync() error
}

type jsonRecorder struct {
	mu     sync.Mutex
	writer SyncWriter
	failed bool
}

// NewJSONRecorder writes one complete JSON line per decision and fsyncs it.
// After a partial write or a failed sync every later record fails too: an audit
// stream with an uncertain tail must not silently resume past it.
func NewJSONRecorder(writer SyncWriter) Recorder {
	return &jsonRecorder{writer: writer}
}

func (r *jsonRecorder) Record(decision Decision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writer == nil || r.failed {
		return errors.New("audit sink is unavailable")
	}
	line, err := json.Marshal(decision)
	if err != nil {
		r.failed = true
		return errors.New("encode audit record")
	}
	line = append(line, '\n')
	n, err := r.writer.Write(line)
	if err != nil || n != len(line) {
		r.failed = true
		return errors.New("write complete audit record")
	}
	if err := r.writer.Sync(); err != nil {
		r.failed = true
		return errors.New("sync audit record")
	}
	return nil
}
