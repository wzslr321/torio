package status

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// The waiting marker is Torio's own convention, documented in
// docs/contracts/status.md and written by a backend's hooks.
//
// It exists because "waiting on a human" is the one state that is not a fact
// anything continuously writes: an agent is waiting only from the moment it
// asks until the moment it is answered, so there is nothing on the guest a poll
// could read that is kept up to date by the waiting itself. It is therefore the
// single event-carried field in the schema, and every rule around it exists to
// bound what a lost or stale event can claim: it ranks below liveness, and it
// expires on its own. The empty document persists as the readiness fact that
// bootstrap and the managed hooks are present.
const (
	// MarkerFileName is the marker, in the backend identity's home. The
	// dot-prefixed `.torio-` name is the same convention every other file Torio
	// owns on a guest follows.
	MarkerFileName = ".torio-waiting.json"
	// MarkerSchemaVersion is the version a marker must declare. A marker
	// written by a hook this binary does not understand is unknown, not a
	// guess.
	MarkerSchemaVersion = "2"
	// MaxMarkerWaits bounds both the document and the work needed to rank it.
	// Session ids are bounded separately, so the complete marker stays small.
	MaxMarkerWaits = 64
	// MarkerTTL is how long a marker is trusted, measured from its modification
	// time.
	//
	// The failure it bounds is asymmetric on purpose. A marker nobody cleared —
	// because the process died between asking and being answered — would
	// otherwise stay on the surface forever, and an operator who has learned to
	// ignore one stale plea ignores the real one next to it. An hour is long
	// enough that a genuine wait outlives a meeting and short enough that a
	// stale one is gone by the next time the operator looks.
	MarkerTTL = time.Hour
)

// markerDoc is the marker's wire shape. It deliberately carries no free-text
// field: there is nothing an agent could write here that a renderer would print.
type markerDoc struct {
	SchemaVersion string       `json:"schema_version"`
	Waits         []markerWait `json:"waits,omitempty"`
}

type markerWait struct {
	SessionID string `json:"session_id"`
	PID       int    `json:"pid"`
	SinceUnix int64  `json:"since_unix"`
}

// decodeMarker decodes one marker document, rejecting unknown fields and
// anything after it.
//
// This is a local copy of a decoder three other packages also keep, and it
// stays local for the reason each of those does: every one of them decides
// something a caller then fails closed on, and a shared helper that grew a
// lenient mode for one caller would quietly change what the others prove. Here
// the thing being decided is whether an operator is told someone is waiting for
// them, from a file written on the untrusted side of the VM boundary.
func decodeMarker(data []byte) (markerDoc, error) {
	var doc markerDoc
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return markerDoc{}, fmt.Errorf("invalid JSON or unknown field: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return markerDoc{}, errors.New("unexpected trailing data after JSON document")
	}
	if doc.SchemaVersion != MarkerSchemaVersion {
		return markerDoc{}, fmt.Errorf("unsupported marker schema version %q", doc.SchemaVersion)
	}
	if doc.Waits == nil || len(doc.Waits) > MaxMarkerWaits {
		return markerDoc{}, errors.New("invalid marker shape")
	}
	seen := make(map[string]struct{}, len(doc.Waits))
	for _, wait := range doc.Waits {
		if !validSessionID(wait.SessionID) {
			return markerDoc{}, errors.New("invalid marker session id")
		}
		if _, exists := seen[wait.SessionID]; exists {
			return markerDoc{}, errors.New("duplicate marker session id")
		}
		seen[wait.SessionID] = struct{}{}
		if wait.PID <= 0 || wait.SinceUnix <= 0 {
			return markerDoc{}, errors.New("invalid marker wait")
		}
	}
	return doc, nil
}

func validSessionID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// markerTrusted reports whether the marker file itself may be read.
//
// This is an operational drift detector, not a boundary: the backend identity
// owns its home and can forge or remove its own marker. Ownership and mode are
// still checked before content is fetched so a different guest identity cannot
// speak for it. A marker that fails the gate is unknown rather than absent,
// because "someone else could have written this" and "nobody is waiting" are
// not the same answer. Age belongs to each wait, not to the persistent empty
// readiness document.
func markerTrusted(e statEntry, owner string) bool {
	return e.owner == owner && !writableBeyondOwner(e.mode)
}

// waitingFromMarker turns a trusted, decoded marker into the reported field,
// ranked below liveness.
//
// Liveness wins in both directions and that is the point. A marker naming a pid
// that is gone reports not-waiting: the agent that asked has died, and nobody
// is coming to answer a question nobody is asking any more. The wait must also
// be no older than the current process start, so a reused pid cannot revive it.
func waitingFromMarker(doc markerDoc, sessions []Session, guestNow time.Time) WaitingField {
	live := make(map[int]int64, len(sessions))
	for _, session := range sessions {
		live[session.PID] = guestNow.Unix() - session.AgeSeconds
	}
	waits := make([]Wait, 0, len(doc.Waits))
	expired := false
	for _, marker := range doc.Waits {
		startedAt, ok := live[marker.PID]
		if !ok || marker.SinceUnix < startedAt {
			continue
		}
		age := guestNow.Unix() - marker.SinceUnix
		if age < 0 || age > int64(MarkerTTL/time.Second) {
			expired = true
			continue
		}
		waits = append(waits, Wait{
			SessionID:  marker.SessionID,
			PID:        marker.PID,
			AgeSeconds: age,
		})
	}
	if len(waits) == 0 {
		if expired {
			return unknownWaiting()
		}
		return WaitingField{State: Known, Waiting: false, Waits: []Wait{}}
	}
	return WaitingField{
		State:   Known,
		Waiting: true,
		Waits:   waits,
	}
}
