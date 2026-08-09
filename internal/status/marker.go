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
	// legacyMarkerSchemaVersion is the single-session shape written by the
	// unmerged first implementation. Reading it gives an existing development
	// box an honest transition while bootstrap reports and replaces the old
	// helper; new code never writes it.
	legacyMarkerSchemaVersion = "1"
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

// The kinds a marker may declare. They are a closed set because this value
// reaches a rendered line: an agent that could put arbitrary text here would be
// writing directly into the operator's terminal.
const (
	// KindPermission is an agent blocked on a permission decision.
	KindPermission = "permission"
	// KindNotification is an agent asking for attention for any other reason.
	KindNotification = "notification"
)

// markerDoc is the marker's wire shape. It deliberately carries no free-text
// field: there is nothing an agent could write here that a renderer would print.
type markerDoc struct {
	SchemaVersion string `json:"schema_version"`
	Kind          string `json:"kind,omitempty"`
	// PID is the process that is waiting, zero when the hook did not record
	// one. It is what ranks the marker below liveness.
	PID   int          `json:"pid,omitempty"`
	Waits []markerWait `json:"waits,omitempty"`
}

type markerWait struct {
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"`
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
	switch doc.SchemaVersion {
	case legacyMarkerSchemaVersion:
		if doc.Waits != nil || !knownMarkerKind(doc.Kind) || doc.PID < 0 {
			return markerDoc{}, errors.New("invalid legacy marker shape")
		}
	case MarkerSchemaVersion:
		if doc.Kind != "" || doc.PID != 0 || doc.Waits == nil {
			return markerDoc{}, errors.New("invalid aggregate marker shape")
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
			if !knownMarkerKind(wait.Kind) || wait.PID <= 0 || wait.SinceUnix <= 0 {
				return markerDoc{}, errors.New("invalid marker wait")
			}
		}
	default:
		return markerDoc{}, fmt.Errorf("unsupported marker schema version %q", doc.SchemaVersion)
	}
	return doc, nil
}

func knownMarkerKind(kind string) bool {
	return kind == KindPermission || kind == KindNotification
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
// is coming to answer a question nobody is asking any more. A marker naming no
// pid is read against the box as a whole, so it survives only while something
// is still running there.
func waitingFromMarker(doc markerDoc, e statEntry, sessions []Session, guestNow time.Time) WaitingField {
	if doc.SchemaVersion == MarkerSchemaVersion {
		return waitingFromAggregateMarker(doc, sessions, guestNow)
	}
	markerAge := guestNow.Sub(e.mtime)
	if markerAge < 0 || markerAge > MarkerTTL {
		return unknownWaiting()
	}
	alive := false
	if doc.PID == 0 {
		alive = len(sessions) > 0
	} else {
		for _, s := range sessions {
			if s.PID == doc.PID {
				alive = true
				break
			}
		}
	}
	if !alive {
		return WaitingField{State: Known, Waiting: false, Waits: []Wait{}}
	}
	wait := Wait{
		Kind:       doc.Kind,
		PID:        doc.PID,
		AgeSeconds: int64(markerAge / time.Second),
	}
	return WaitingField{
		State:      Known,
		Waiting:    true,
		Waits:      []Wait{wait},
		Kind:       doc.Kind,
		PID:        doc.PID,
		AgeSeconds: wait.AgeSeconds,
	}
}

func waitingFromAggregateMarker(doc markerDoc, sessions []Session, guestNow time.Time) WaitingField {
	live := make(map[int]struct{}, len(sessions))
	for _, session := range sessions {
		live[session.PID] = struct{}{}
	}
	waits := make([]Wait, 0, len(doc.Waits))
	expired := false
	for _, marker := range doc.Waits {
		if _, ok := live[marker.PID]; !ok {
			continue
		}
		age := guestNow.Unix() - marker.SinceUnix
		if age < 0 || age > int64(MarkerTTL/time.Second) {
			expired = true
			continue
		}
		waits = append(waits, Wait{
			SessionID:  marker.SessionID,
			Kind:       marker.Kind,
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
	first := waits[0]
	return WaitingField{
		State:      Known,
		Waiting:    true,
		Waits:      waits,
		Kind:       first.Kind,
		PID:        first.PID,
		AgeSeconds: first.AgeSeconds,
	}
}
