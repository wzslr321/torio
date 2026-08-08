package mcpbroker

import (
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// AuditRecord contains only the decision metadata ADR-0012 permits. It has no
// field capable of holding tool arguments, results, protocol bodies or tokens.
type AuditRecord struct {
	Time    time.Time `json:"time"`
	PeerUID uint32    `json:"peer_uid"`
	Service string    `json:"service"`
	Tool    string    `json:"tool"`
	Writes  bool      `json:"writes"`
	Allowed bool      `json:"allowed"`
}

func newAuditRecord(service, tool string, uid uint32, writes, allowed bool) AuditRecord {
	return AuditRecord{
		Time:    time.Now().UTC(),
		PeerUID: uid,
		Service: service,
		Tool:    tool,
		Writes:  writes,
		Allowed: allowed,
	}
}

// SyncWriter is the append-only sink needed for durable audit decisions.
// *os.File implements it.
type SyncWriter interface {
	io.Writer
	Sync() error
}

type jsonRecorder struct {
	mu     sync.Mutex
	writer SyncWriter
	failed bool
}

// NewJSONRecorder serializes complete JSON lines and fsyncs every decision.
// Once a write is partial or a sync fails, all later records fail closed: an
// audit stream with an uncertain tail must not silently resume after it.
func NewJSONRecorder(writer SyncWriter) Recorder {
	return &jsonRecorder{writer: writer}
}

func (r *jsonRecorder) Record(record AuditRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writer == nil || r.failed {
		return errors.New("audit sink is unavailable")
	}
	line, err := json.Marshal(record)
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
