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
// structurally rather than trusted to review. ADR-0022 §5 fixes the fields an
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
	return AuditRecord{
		Time:    time.Date(2026, 7, 29, 11, 4, 5, 0, time.UTC),
		Service: service,
		Tool:    tool,
		UID:     uid,
		Allowed: allowed,
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
		`{"ts":"2026-07-29T11:04:05Z","service":"atlassian","tool":"getJiraIssue","uid":1001,"decision":"allow"}`,
		`{"ts":"2026-07-29T11:04:05Z","service":"atlassian","tool":"deleteJiraProject","uid":1001,"decision":"deny"}`,
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
	want := []string{"ts", "service", "tool", "uid", "decision"}
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
