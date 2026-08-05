package mcpbroker

import (
	"bytes"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestAuditRecordCannotCarryContent is the point of the type, asserted
// structurally rather than trusted to review. ADR-0004 §5 fixes the fields an
// audit line may hold — timestamp, service, tool, calling uid, decision — and
// forbids arguments and response bodies, because upstream Jira and Confluence
// content would otherwise land in a log that outlives the call.
//
// The test therefore checks two things a doc comment cannot enforce: that the
// field set is exactly the permitted one, and that no field has a type able to
// hold arbitrary structure. A map, slice, interface or pointer field would be a
// place to put a payload, whatever it was named.
func TestAuditRecordCannotCarryContent(t *testing.T) {
	rt := reflect.TypeOf(AuditRecord{})

	want := map[string]reflect.Kind{
		"Time":    reflect.Struct, // time.Time
		"Service": reflect.String,
		"Tool":    reflect.String,
		"UID":     reflect.Uint32,
		"Allowed": reflect.Bool,
		// Reason is admitted on the same terms as the rest: a Uint8 over a closed
		// enum can hold one of a fixed set of tokens and nothing else. It is here
		// because a denial cannot be triaged once the policy file has been edited,
		// not because it was convenient. Any further field must clear the same bar.
		"Reason": reflect.Uint8,
	}

	if rt.NumField() != len(want) {
		var got []string
		for i := range rt.NumField() {
			got = append(got, rt.Field(i).Name)
		}
		t.Fatalf("AuditRecord fields = %v, want exactly %d: %v", got, len(want), want)
	}

	for i := range rt.NumField() {
		f := rt.Field(i)
		kind, permitted := want[f.Name]
		if !permitted {
			t.Errorf("AuditRecord has field %q; an audit line may carry only %v", f.Name, want)
			continue
		}
		if f.Type.Kind() != kind {
			t.Errorf("field %s is %s, want %s", f.Name, f.Type.Kind(), kind)
		}
		switch f.Type.Kind() {
		case reflect.Map, reflect.Slice, reflect.Array, reflect.Interface, reflect.Pointer, reflect.UnsafePointer:
			t.Errorf("field %s is a %s and could hold a payload", f.Name, f.Type.Kind())
		}
		if !f.IsExported() {
			t.Errorf("field %s is unexported; a hidden field is a place to put content", f.Name)
		}
	}
}

// decisionAt builds a record with a fixed instant, so a rendered line is
// comparable byte for byte.
func decisionAt(service, tool string, uid uint32, allowed bool) AuditRecord {
	reason := ReasonToolNotGranted
	if allowed {
		reason = ReasonGranted
	}
	return AuditRecord{
		Time:    time.Date(2026, 7, 29, 11, 4, 5, 0, time.UTC),
		Service: service,
		Tool:    tool,
		UID:     uid,
		Allowed: allowed,
		Reason:  reason,
	}
}

func TestWriteAuditRendersOneLinePerRecord(t *testing.T) {
	var buf bytes.Buffer

	if err := WriteAudit(&buf, decisionAt("atlassian", "getJiraIssue", 1001, true)); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}
	if err := WriteAudit(&buf, decisionAt("atlassian", "deleteJiraProject", 1001, false)); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want one per record:\n%s", len(lines), buf.String())
	}

	want := []string{
		`{"ts":"2026-07-29T11:04:05Z","service":"atlassian","tool":"getJiraIssue","uid":1001,"decision":"allow","reason":"granted"}`,
		`{"ts":"2026-07-29T11:04:05Z","service":"atlassian","tool":"deleteJiraProject","uid":1001,"decision":"deny","reason":"tool_not_granted"}`,
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d =\n  %s\nwant\n  %s", i, lines[i], w)
		}
	}
}

// TestWriteAuditRendersOnlyThePermittedKeys is the wire-level half of
// TestAuditRecordCannotCarryContent: the Go type cannot hold a payload, and the
// line it produces has nowhere to put one either.
func TestWriteAuditRendersOnlyThePermittedKeys(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAudit(&buf, decisionAt("atlassian", "getJiraIssue", 1001, true)); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("audit line is not one JSON object: %v", err)
	}
	// "reason" was added deliberately: it is a closed enum, carries no payload,
	// and a denial cannot be triaged after the policy has been edited without it.
	// Every other addition must be argued the same way — that is what this
	// assertion is for.
	want := []string{"ts", "service", "tool", "uid", "decision", "reason"}
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want exactly %v", slices.Sorted(maps.Keys(got)), want)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("audit line is missing key %q", k)
		}
	}
}

// TestWriteAuditBoundsCallerSuppliedNames covers the case the policy loader
// cannot: a *denied* call names a service and a tool that no policy document
// ever validated, because the caller made them up. An unbounded name would let
// the agent write as much as it likes into the broker's log by calling a tool
// that does not exist.
//
// The over-long name is truncated rather than dropped, and the record is still
// written. Losing the evidence of a call is worse than logging a shortened name:
// the whole point of the line is that the attempt happened.
func TestWriteAuditBoundsCallerSuppliedNames(t *testing.T) {
	rec := decisionAt(strings.Repeat("s", 4096), strings.Repeat("t", 4096), 1001, false)

	var buf bytes.Buffer
	if err := WriteAudit(&buf, rec); err != nil {
		t.Fatalf("a hostile name must not stop the decision being recorded: %v", err)
	}
	if buf.Len() > 1024 {
		t.Errorf("audit line is %d bytes; a caller must not be able to choose the size of the log", buf.Len())
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bounded line is not valid JSON: %v", err)
	}
	if s := got["service"].(string); len(s) > maxAuditFieldLen+utf8.UTFMax {
		t.Errorf("service field is %d bytes, want it bounded to about %d", len(s), maxAuditFieldLen)
	}
	if got["decision"] != "deny" {
		t.Errorf("decision = %v, want the verdict preserved", got["decision"])
	}
}

// TestWriteAuditCannotForgeALine is the injection case. A caller that can put a
// newline into a tool name could otherwise append a second, invented record —
// one that says a call was allowed. One record must always be one line.
func TestWriteAuditCannotForgeALine(t *testing.T) {
	forged := "x\n" + `{"ts":"2026-07-29T11:04:05Z","service":"atlassian","tool":"getJiraIssue","uid":0,"decision":"allow"}`

	var buf bytes.Buffer
	if err := WriteAudit(&buf, decisionAt("atlassian", forged, 1001, false)); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}

	if n := strings.Count(strings.TrimSuffix(buf.String(), "\n"), "\n"); n != 0 {
		t.Fatalf("one record produced %d extra lines:\n%s", n, buf.String())
	}
	if strings.Contains(strings.TrimSuffix(buf.String(), "\n"), `"decision":"allow"`) {
		t.Errorf("a denied record rendered an allow verdict:\n%s", buf.String())
	}
}

// TestWriteAuditRejectsRecordWithoutTimestamp refuses the one field a caller
// cannot get wrong by accident. An undated line is not evidence, and unlike a
// hostile tool name — which comes from outside and must still be recorded — a
// zero Time can only be a bug in the broker itself.
func TestWriteAuditRejectsRecordWithoutTimestamp(t *testing.T) {
	rec := decisionAt("atlassian", "getJiraIssue", 1001, true)
	rec.Time = time.Time{}

	var buf bytes.Buffer
	if err := WriteAudit(&buf, rec); err == nil {
		t.Fatalf("a record with no timestamp must be rejected")
	}
	if buf.Len() != 0 {
		t.Errorf("rejected record still wrote %q", buf.String())
	}
}

// TestAuditRecordsTheDenialReason: the reason is derivable from the policy
// documents only if you hold the policy as it was when the decision was taken,
// and nobody keeps that. Recorded at decision time it is evidence; reconstructed
// afterwards from a file that has since been edited it is a guess.
//
// It also separates two denials that mean different things operationally: a
// call addressed to a service that was never configured reads as probing, a
// call naming a tool outside an existing grant reads as an agent being steered
// past its grant. Both are one fixed token, so neither can carry a payload.
func TestAuditRecordsTheDenialReason(t *testing.T) {
	cases := []struct {
		name   string
		rec    AuditRecord
		expect string
	}{
		{"unknown service", AuditRecord{Time: auditFixedTime(), Service: "nope", Tool: "search", Reason: ReasonUnknownService}, "unknown_service"},
		{"tool not granted", AuditRecord{Time: auditFixedTime(), Service: "atlassian", Tool: "createJiraIssue", Reason: ReasonToolNotGranted}, "tool_not_granted"},
		{"granted", AuditRecord{Time: auditFixedTime(), Service: "atlassian", Tool: "search", Allowed: true, Reason: ReasonGranted}, "granted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteAudit(&buf, tc.rec); err != nil {
				t.Fatalf("WriteAudit: %v", err)
			}
			var line map[string]any
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("audit line is not one JSON document: %v (%q)", err, buf.String())
			}
			if line["reason"] != tc.expect {
				t.Errorf("reason = %v, want %q (line: %s)", line["reason"], tc.expect, buf.String())
			}
		})
	}
}

// TestAuditReasonIsAFixedToken keeps the reason from becoming a place a payload
// can hide: it is rendered from a closed enum, so an out-of-range value must not
// reach the line as caller-influenced text.
func TestAuditReasonIsAFixedToken(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAudit(&buf, AuditRecord{Time: auditFixedTime(), Service: "s", Tool: "t", Reason: Reason(200)}); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("audit line is not one JSON document: %v", err)
	}
	got, _ := line["reason"].(string)
	for _, known := range []string{"unknown_service", "tool_not_granted", "granted", "unknown"} {
		if got == known {
			return
		}
	}
	t.Errorf("reason = %q, want one of the closed token set", got)
}

// auditFixedTime is a stable instant for audit-rendering tests: the package
// refuses a record with no timestamp, and a test that supplied "now" would make
// its own output unreproducible.
func auditFixedTime() time.Time {
	return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
}
