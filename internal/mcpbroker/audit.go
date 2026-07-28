package mcpbroker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"
)

// AuditRecord is one broker decision, and the whole of what a broker log may say
// about it: when, which service, which tool, which caller, allowed or denied.
//
// There is no field for tool arguments and none for the upstream response, and
// that absence is the design. A granted read tool returns Jira issues and
// Confluence pages; a log that recorded arguments or bodies would quietly turn
// the audit trail into a second, unmanaged copy of the content the broker exists
// to mediate — durable, readable by whoever reads logs, and outliving the call
// that produced it. ADR-0022 §5 states the rule, and it is the same one
// docs/contracts/cli.md imposes on Brain output, where reporting is restricted to
// bounded aggregate metadata and never note names or note content.
//
// Enforcing it structurally rather than by convention is deliberate. A field
// that could hold arbitrary content — a map, a slice, an `any` — would be filled
// eventually, by someone debugging something at 2am with a good reason. There is
// nowhere to put it.
//
// A denied call is audited exactly like an allowed one, and its Service and Tool
// come from the caller rather than from any policy this package validated. They
// are therefore treated as untrusted when rendered (see WriteAudit).
type AuditRecord struct {
	// Time is when the decision was taken.
	Time time.Time
	// Service is the service the call was addressed to.
	Service string
	// Tool is the tool the call named.
	Tool string
	// UID is the calling identity as the kernel reports it over the unix socket
	// (SO_PEERCRED), never a value the caller claims. That is what makes the line
	// evidence rather than a note: ADR-0022 puts identity in the kernel precisely
	// so no presented secret can stand in for it.
	UID uint32
	// Allowed is the verdict.
	Allowed bool
	// Reason is why. It is recorded rather than left to be derived from the
	// policy documents, because deriving it needs the policy as it stood when
	// the decision was taken and nothing keeps that: after one edit the
	// reconstruction is a guess. It also separates two denials that mean
	// different things — a call addressed to a service that was never configured
	// reads as probing, a call naming a tool outside an existing grant reads as
	// an agent being steered past its grant.
	//
	// It is safe to record precisely because it is a closed enum: one of a fixed
	// set of tokens, never caller text, so it cannot become somewhere a payload
	// hides.
	Reason Reason
}

// maxAuditFieldLen bounds a caller-supplied name in a rendered audit line. A
// granted tool name is already bounded by maxToolNameLen at load; this bound
// exists for the denied call, whose service and tool were never in any policy
// document and are whatever the caller sent.
const maxAuditFieldLen = 128

// auditTruncationMarker ends a name that was cut, so a shortened value is never
// mistaken for the whole one.
const auditTruncationMarker = "…"

// auditLineJSON is the wire form of an AuditRecord: five keys, fixed, in a
// stable order. It is unexported so no caller can construct a line this package
// did not render, and so the permitted key set is one edit away from the
// permitted field set rather than something a struct tag could drift from.
type auditLineJSON struct {
	Timestamp string `json:"ts"`
	Service   string `json:"service"`
	Tool      string `json:"tool"`
	UID       uint32 `json:"uid"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
}

// WriteAudit renders rec as one JSON object on one line of w.
//
// One record per line, JSON, because the log is meant to be read by an operator
// and grepped by a machine without either needing a parser for it. Timestamps
// are RFC 3339 in UTC: the guest, the host and whoever reads the log later must
// not have to agree on a timezone to order two lines.
//
// Service and Tool are bounded before rendering, and the JSON encoder escapes
// every control byte in them. Both matter for a denied call: its names came from
// the caller and were never checked against a policy document, so without the
// bound a caller could choose how much the broker logs, and without the escaping
// it could put a newline in a tool name and append a line of its own saying the
// call was allowed. Truncation is the deliberate choice over rejection — a
// shortened name still records that the attempt happened, and losing the record
// is the worse failure.
//
// A zero Time is the exception, and is rejected. It cannot come from outside;
// it is a broker that forgot to stamp its own record, and an undated line is not
// evidence of anything.
//
// Rotation, file ownership and where the log actually lives are all deliberately
// not here. This function renders a record; the broker's unit decides what it
// renders into.
func WriteAudit(w io.Writer, rec AuditRecord) error {
	if rec.Time.IsZero() {
		return errors.New("audit record has no timestamp")
	}

	decision := "deny"
	if rec.Allowed {
		decision = "allow"
	}
	// A verdict and its reason must agree. An AuditRecord whose Allowed is true
	// while Reason still holds its zero value would render an "allow" line
	// reading "unknown_service" — a line that is worse than no line, because an
	// operator would reasonably believe it. Refuse it instead of rendering it.
	if rec.Allowed != (rec.Reason == ReasonGranted) {
		return fmt.Errorf("audit record is incoherent: allowed=%t with reason %q", rec.Allowed, auditReason(rec.Reason))
	}

	line := auditLineJSON{
		Timestamp: rec.Time.UTC().Format(time.RFC3339),
		Service:   boundAuditField(rec.Service),
		Tool:      boundAuditField(rec.Tool),
		UID:       rec.UID,
		Decision:  decision,
		Reason:    auditReason(rec.Reason),
	}

	return json.NewEncoder(w).Encode(line)
}

// auditReason renders the decision reason as one of a closed token set. An
// out-of-range value becomes "unknown" rather than reaching the line as
// anything caller-influenced: the field's safety rests entirely on it being a
// fixed vocabulary, so an unrecognised value must degrade to another fixed
// token, never to free text.
func auditReason(r Reason) string {
	switch r {
	case ReasonUnknownService, ReasonToolNotGranted, ReasonGranted:
		return r.String()
	default:
		return "unknown"
	}
}

// boundAuditField truncates a caller-supplied name to maxAuditFieldLen bytes,
// marking it when it cuts.
//
// The cut is made on a rune boundary so the result stays valid UTF-8 and the
// encoder does not have to substitute replacement characters for a rune this
// function sliced in half.
func boundAuditField(s string) string {
	if len(s) <= maxAuditFieldLen {
		return s
	}
	cut := maxAuditFieldLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + auditTruncationMarker
}
