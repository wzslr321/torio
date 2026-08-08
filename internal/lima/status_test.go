package lima

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

func TestStatusNotFoundEmptyList(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("")},
	}}
	a := New(fr)

	got, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if got.State != StateNotFound {
		t.Fatalf("State = %v, want %v", got.State, StateNotFound)
	}

	args := fr.callArgs(0)
	want := []string{"list", "--json", "--tty=false"}
	if !equalArgs(args, want) {
		t.Fatalf("argv = %v, want %v", args, want)
	}
}

func TestStatusRunning(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))},
	}}
	a := New(fr)

	got, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if got.State != StateRunning {
		t.Fatalf("State = %v, want %v", got.State, StateRunning)
	}
}

func TestStatusStopped(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Stopped"))},
	}}
	a := New(fr)

	got, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if got.State != StateStopped {
		t.Fatalf("State = %v, want %v", got.State, StateStopped)
	}
}

func TestStatusIgnoresOtherInstances(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON("some-other-vm", "Running"))},
	}}
	a := New(fr)

	got, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if got.State != StateNotFound {
		t.Fatalf("State = %v, want %v (unrelated instance must not match)", got.State, StateNotFound)
	}
}

func TestStatusMultipleInstancesNDJSON(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON("some-other-vm", "Running") + "\n" + fixtureInstanceJSON(InstanceName, "Stopped") + "\n")},
	}}
	a := New(fr)

	got, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if got.State != StateStopped {
		t.Fatalf("State = %v, want %v", got.State, StateStopped)
	}
}

func TestStatusMalformedJSON(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("{not valid json")},
	}}
	a := New(fr)

	_, err := a.Status(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindMalformedOutput {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindMalformedOutput)
	}
}

func TestStatusUnrecognizedStatusString(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "SomethingNewFutureLimaAdded"))},
	}}
	a := New(fr)

	_, err := a.Status(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindMalformedOutput {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindMalformedOutput)
	}
}

func TestStatusListCommandFailed(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: exitResult(1, "", "lima: internal error")},
	}}
	a := New(fr)

	_, err := a.Status(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindCommandFailed {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindCommandFailed)
	}
}

func TestStatusListTimeout(t *testing.T) {
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: -1}, wrapErr(context.DeadlineExceeded)
	}}
	a := New(fr)

	_, err := a.Status(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindTimeout {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindTimeout)
	}
}

func TestStatusRejectsTruncatedListOutput(t *testing.T) {
	// execx bounds retained child output and flags truncation. A truncated
	// `limactl list --json` stream may have been cut mid-record, so a "not
	// found" or a partially-decoded state read from it is untrustworthy: fail
	// closed instead of interpreting a truncated list as ground truth.
	fr := &fakeRunner{script: []scriptedResponse{
		{result: execx.Result{
			ExitCode:        0,
			Stdout:          []byte(fixtureInstanceJSON(InstanceName, "Running")),
			StdoutTruncated: true,
		}},
	}}
	a := New(fr)

	_, err := a.Status(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("Status must fail closed on truncated list output; got err=%v", err)
	}
	if lerr.Kind != KindMalformedOutput {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindMalformedOutput)
	}
}

func TestStatusRejectsRecordWithEmptyName(t *testing.T) {
	// A record whose required "name" is empty is semantically incomplete.
	// Silently skipping it (and thus reporting StateNotFound) would let a
	// malformed list masquerade as "no VM". It must fail closed.
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON("", "Running"))},
	}}
	a := New(fr)

	_, err := a.Status(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("Status must fail closed on a record with empty name; got err=%v", err)
	}
	if lerr.Kind != KindMalformedOutput {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindMalformedOutput)
	}
}

func TestStatusRejectsRecordWithEmptyStatus(t *testing.T) {
	// Symmetric to empty-name: a record present in the list but carrying no
	// "status" is incomplete and must not be interpreted (as our instance or
	// as absence). Uses a non-matching name so this specifically exercises the
	// per-record completeness check, not the matched-instance status mapping.
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON("some-other-vm", ""))},
	}}
	a := New(fr)

	_, err := a.Status(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("Status must fail closed on a record with empty status; got err=%v", err)
	}
	if lerr.Kind != KindMalformedOutput {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindMalformedOutput)
	}
}

// TestListTorioInstancesReturnsOnlyTorioOwnedBoxes pins what a status poll is
// allowed to claim is Torio's. Ownership is decided by name, so the default
// instance and every derived one are in, and a neighbouring VM the operator
// runs for something else is not — reporting it would say an agent is not
// running on a box that never had one.
func TestListTorioInstancesReturnsOnlyTorioOwnedBoxes(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(strings.Join([]string{
			fixtureInstanceJSON("torio-claude-code", "Running"),
			fixtureInstanceJSON("some-other-vm", "Running"),
			fixtureInstanceJSON("torio", "Stopped"),
		}, "\n"))},
	}}
	a := New(fr)

	got, err := a.ListTorioInstances(context.Background())
	if err != nil {
		t.Fatalf("ListTorioInstances: unexpected error: %v", err)
	}
	want := []InstanceInfo{
		{Name: "torio", State: StateStopped, RawStatus: "Stopped"},
		{Name: "torio-claude-code", State: StateRunning, RawStatus: "Running"},
	}
	if len(got) != len(want) {
		t.Fatalf("instances = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("instances[%d] = %v, want %v", i, got[i], want[i])
		}
	}

	args := fr.callArgs(0)
	if !equalArgs(args, []string{"list", "--json", "--tty=false"}) {
		t.Fatalf("argv = %v, want the host-side list command", args)
	}
}

// TestListTorioInstancesIncludesAnExplicitlyNamedBox covers the gap the derived
// prefix leaves: TORIO_INSTANCE names a box directly, and such a box carries no
// name Torio could recognize. The caller passes the instance it resolved, and a
// poll that skipped it would report nothing about the very box the operator is
// working in.
func TestListTorioInstancesIncludesAnExplicitlyNamedBox(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(strings.Join([]string{
			fixtureInstanceJSON("scratch-box", "Running"),
			fixtureInstanceJSON("another-vm", "Running"),
		}, "\n"))},
	}}
	a := New(fr)

	got, err := a.ListTorioInstances(context.Background(), "scratch-box")
	if err != nil {
		t.Fatalf("ListTorioInstances: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "scratch-box" {
		t.Fatalf("instances = %v, want only the explicitly named box", got)
	}
}

// TestListTorioInstancesSkipsAnInvalidName pins that a name from external tool
// output is validated before it is returned. A name Torio could not have
// created is not Torio's box, and it would otherwise reach a `limactl` argv and
// a rendered line.
func TestListTorioInstancesSkipsAnInvalidName(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(strings.Join([]string{
			fixtureInstanceJSON("torio-Bad_Name", "Running"),
			fixtureInstanceJSON("torio", "Running"),
		}, "\n"))},
	}}
	a := New(fr)

	got, err := a.ListTorioInstances(context.Background())
	if err != nil {
		t.Fatalf("ListTorioInstances: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "torio" {
		t.Fatalf("instances = %v, want the invalid name skipped", got)
	}
}

// TestListTorioInstancesFailsClosedOnUnrecognizedStatus mirrors Status: a status
// string Torio does not recognize means its model of limactl is wrong, which is
// not a fact about one box and must not be rendered as one.
func TestListTorioInstancesFailsClosedOnUnrecognizedStatus(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON("torio", "Hibernating"))},
	}}
	a := New(fr)

	_, err := a.ListTorioInstances(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("ListTorioInstances must fail closed on an unrecognized status; got err=%v", err)
	}
	if lerr.Kind != KindMalformedOutput {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindMalformedOutput)
	}
}

func TestListTorioInstancesRejectsTruncatedListOutput(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: execx.Result{
			ExitCode:        0,
			Stdout:          []byte(fixtureInstanceJSON("torio", "Running")),
			StdoutTruncated: true,
		}},
	}}
	a := New(fr)

	_, err := a.ListTorioInstances(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("ListTorioInstances must fail closed on truncated list output; got err=%v", err)
	}
	if lerr.Kind != KindMalformedOutput {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindMalformedOutput)
	}
}

// fixtureInstanceJSON returns a single-line `limactl list --json` record for
// name/status, shaped like the real output captured from `limactl list
// --json` against a live Lima 2.2.0 instance: one object per line with no
// enclosing array, `name` and `status` at the top level, and the rest of the
// instance under a nested `config`. The V1 adapter reads only the top-level
// name/status; the surrounding fields mirror the real record so the fixture
// stays representative of the actual NDJSON shape rather than of the minimum
// that happens to parse.
func fixtureInstanceJSON(name, status string) string {
	return `{"name":"` + name + `","hostname":"lima-` + name + `","status":"` + status + `","dir":"/Users/USER/.lima/` + name + `","vmType":"vz","arch":"aarch64","cpus":4,"memory":8589934592,"disk":64424509440,"sshLocalPort":54250,"config":{"vmType":"vz","os":"Linux","arch":"aarch64","cpus":4,"memory":"8GiB","disk":"60GiB"},"sshAddress":"127.0.0.1","protected":false,"limaVersion":"2.2.0"}`
}
