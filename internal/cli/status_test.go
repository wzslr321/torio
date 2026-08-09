package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/status"
)

// statusListJSON is one `limactl list --json` record, in the NDJSON shape real
// output takes.
func statusListJSON(name, state string) string {
	return `{"name":"` + name + `","status":"` + state + `","config":{}}`
}

// A poll over a stopped box is answered entirely from the host: the box state
// proves nothing is running there, so the only guest-facing call is the
// enumeration itself.
func TestStatusReportsAStoppedBoxWithoutEnteringIt(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 0, Stdout: []byte(statusListJSON("torio", "Stopped") + "\n")}},
	}}

	code, stdout, stderr := runVMWithFake(t, []string{"status"}, fake)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout = %q, want a header and one row", stdout)
	}
	if lines[0] != "INSTANCE\tBOX\tBACKEND\tSESSION\tWAITING\tPROGRESS" {
		t.Errorf("header = %q", lines[0])
	}
	want := "torio\tstopped\thermes\t0\tno\t" + glyphUnknown
	if lines[1] != want {
		t.Errorf("row = %q, want %q", lines[1], want)
	}
	if len(fake.calls) != 1 {
		t.Errorf("host calls = %d, want only the enumeration", len(fake.calls))
	}
}

// The surface covers only what Torio owns. A neighbouring VM the operator runs
// for something else is not reported, because reporting it would claim an agent
// is not running on a box that never had one.
func TestStatusReportsOnlyTorioOwnedBoxes(t *testing.T) {
	body := statusListJSON("torio", "Stopped") + "\n" +
		statusListJSON("someone-elses-vm", "Running") + "\n"
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 0, Stdout: []byte(body)}},
	}}

	_, stdout, _ := runVMWithFake(t, []string{"status"}, fake)

	if strings.Contains(stdout, "someone-elses-vm") {
		t.Fatalf("stdout = %q, want the unrelated VM left out", stdout)
	}
}

// No boxes is a complete answer, not an error: nothing has been created yet.
func TestStatusOnAHostWithNoBoxes(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 0, Stdout: nil}},
	}}

	code, stdout, stderr := runVMWithFake(t, []string{"status"}, fake)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "no instances") || !strings.Contains(stdout, "next: torio vm init") {
		t.Errorf("stdout = %q, want an empty answer with a next step", stdout)
	}
}

func TestStatusIgnoresTheInvocationConfigAndDegradesPerBox(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configPath string
		writeRoot  bool
	}{
		{name: "explicit config is not the document for every box", configPath: "/dev/null"},
		{name: "a malformed box document costs only that row", writeRoot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", home)
			if tc.writeRoot {
				dir := filepath.Join(home, "torio")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fake := &fakeLimaRunner{script: []scriptedResp{{
				res: execx.Result{ExitCode: 0, Stdout: []byte(statusListJSON("torio", "Stopped") + "\n")},
			}}}
			var stdout, stderr bytes.Buffer
			a := &app{
				stdout: &stdout,
				stderr: &stderr,
				build:  testBuild(),
				newLima: func() *lima.Adapter {
					return lima.New(fake)
				},
				lookupOperatorUser: func() (string, error) { return "testop", nil },
			}
			args := []string{"status"}
			if tc.configPath != "" {
				args = append(args, "--config", tc.configPath)
			}
			code := runWithApp(context.Background(), a, args)
			if code != int(ExitOK) {
				t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
			}
			if tc.writeRoot && !strings.Contains(stdout.String(), "torio\tstopped\t"+glyphUnknown) {
				t.Errorf("stdout = %q, want one degraded row", stdout.String())
			}
			if !tc.writeRoot && !strings.Contains(stdout.String(), "torio\tstopped\t"+backend.DefaultName) {
				t.Errorf("stdout = %q, want the box-owned default document", stdout.String())
			}
		})
	}
}

func TestStatusJSONEnvelope(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 0, Stdout: []byte(statusListJSON("torio", "Stopped") + "\n")}},
	}}

	code, stdout, stderr := runVMWithFake(t, []string{"status", "--json"}, fake)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	var env struct {
		SchemaVersion string `json:"schema_version"`
		OK            bool   `json:"ok"`
		Command       string `json:"command"`
		Data          struct {
			Instances []struct {
				Name    string `json:"instance"`
				Box     string `json:"box"`
				Backend struct {
					State string `json:"state"`
					Name  string `json:"name"`
				} `json:"backend"`
				Session struct {
					State    string `json:"state"`
					Sessions []any  `json:"sessions"`
				} `json:"session"`
				Waiting struct {
					State string `json:"state"`
				} `json:"waiting"`
				Progress struct {
					State string `json:"state"`
				} `json:"last_progress"`
			} `json:"instances"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not the envelope: %v; got %q", err, stdout)
	}
	if !env.OK || env.Command != "status" || env.SchemaVersion != schemaVersion {
		t.Fatalf("envelope = %+v", env)
	}
	if len(env.Data.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(env.Data.Instances))
	}
	in := env.Data.Instances[0]
	if in.Name != "torio" || in.Box != "stopped" || in.Backend.Name != "hermes" {
		t.Errorf("instance = %+v", in)
	}
	// Every field states which of the three kinds of answer it is, and the
	// session list is an array even when there is nothing in it, so a recipe can
	// count it without first testing the state.
	if in.Session.State == "" || in.Waiting.State == "" || in.Progress.State == "" {
		t.Errorf("instance = %+v, want every field to carry a state", in)
	}
	if in.Session.Sessions == nil {
		t.Error("sessions = null, want an array")
	}
}

// The one failure a poll does not survive: without the list there is nothing to
// report on, and an empty report would read as "no boxes".
func TestStatusFailsWhenTheHostCannotListBoxes(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 1, Stderr: []byte("lima: internal error")}},
	}}

	code, _, stderr := runVMWithFake(t, []string{"status"}, fake)

	if code != int(ExitExternal) {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitExternal, stderr)
	}
}

// A box whose own document cannot be read is one unknown row. The command still
// exits 0: unknown is an answer.
func TestStatusReportsAnUnknownBackendAsARow(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 0, Stdout: []byte(statusListJSON("torio-nosuchbackend", "Stopped") + "\n")}},
	}}

	code, stdout, stderr := runVMWithFake(t, []string{"status"}, fake)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	row := strings.Split(strings.TrimSpace(stdout), "\n")[1]
	if !strings.HasPrefix(row, "torio-nosuchbackend\tstopped\t"+glyphUnknown) {
		t.Errorf("row = %q, want the backend reported as unknown", row)
	}
}

// Unknown stays a successful row, but verbose mode must say which fact could
// not be proven. Otherwise the operator is told to ask for diagnostics and gets
// no explanation for the first field every other guest fact depends on.
func TestStatusVerboseDiagnosesAnUnresolvedBackend(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 0, Stdout: []byte(statusListJSON("torio-nosuchbackend", "Running") + "\n")}},
	}}

	code, _, stderr := runVMWithFake(t, []string{"status", "--verbose"}, fake)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "fact=backend") {
		t.Fatalf("stderr = %q, want the unresolved backend fact diagnosed", stderr)
	}
}

func TestCustomInstanceWithNoBackendDeclarationDefaultsToHermes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	got := (&app{}).resolveBoxBackend("scratch-box")

	if got.Err != nil {
		t.Fatalf("resolveBoxBackend: %v", got.Err)
	}
	if got.Name != backend.DefaultName || got.Backend == nil {
		t.Fatalf("resolution = %+v, want the ADR-0009 default backend", got)
	}
}

func TestCompactAge(t *testing.T) {
	for _, tc := range []struct {
		seconds int64
		want    string
	}{
		{0, "0s"},
		{45, "45s"},
		{60, "1m"},
		{3599, "59m"},
		{3600, "1h"},
		{86_399, "23h"},
		{86_400, "1d"},
	} {
		if got := compactAge(tc.seconds); got != tc.want {
			t.Errorf("compactAge(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func TestWaitingCellNamesOneSessionAndCountsTheRest(t *testing.T) {
	f := status.WaitingField{
		State:      status.Known,
		Waiting:    true,
		Kind:       "notification",
		PID:        1234,
		AgeSeconds: 420,
		Waits: []status.Wait{
			{SessionID: "a", Kind: "notification", PID: 1234, AgeSeconds: 420},
			{SessionID: "b", Kind: "permission", PID: 1235, AgeSeconds: 30},
		},
	}
	if got, want := waitingCell(f), "notification 7m pid 1234 +1"; got != want {
		t.Fatalf("waiting cell = %q, want %q", got, want)
	}
}

// The status command is registered on the root, so it is reachable as
// `torio status` and carries its own envelope command name.
func TestStatusIsARootCommand(t *testing.T) {
	root := newRootCmd(&app{stdout: io.Discard, stderr: io.Discard, build: testBuild()})
	for _, c := range root.Commands() {
		if c.Name() == "status" {
			return
		}
	}
	t.Fatal("torio status is not registered on the root command")
}
