package mcpbroker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	syncs int
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Sync() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.syncs++
	return nil
}

func TestJSONRecorderWritesCompleteSyncedDecisionLines(t *testing.T) {
	sink := &syncBuffer{}
	recorder := NewJSONRecorder(sink)

	const count = 24
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := recorder.Record(AuditRecord{
				Time:    time.Date(2026, 8, 9, 12, 0, i, 0, time.UTC),
				PeerUID: 1001,
				Service: "tickets",
				Tool:    "read_ticket",
				Allowed: true,
			}); err != nil {
				t.Errorf("Record: %v", err)
			}
		}()
	}
	wg.Wait()

	sink.mu.Lock()
	data := append([]byte(nil), sink.buf.Bytes()...)
	syncs := sink.syncs
	sink.mu.Unlock()
	if syncs != count {
		t.Fatalf("sync count = %d, want %d", syncs, count)
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	lines := 0
	for scanner.Scan() {
		lines++
		var got AuditRecord
		if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
			t.Fatalf("audit line %d is interleaved or invalid: %v", lines, err)
		}
		if got.Tool != "read_ticket" || !got.Allowed {
			t.Fatalf("audit line %d = %#v", lines, got)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if lines != count {
		t.Fatalf("audit lines = %d, want %d", lines, count)
	}
}
