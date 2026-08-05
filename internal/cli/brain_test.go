package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/brain"
	"github.com/wzslr321/torio/internal/lima"
)

type fakeBrainService struct {
	initReport   brain.InitReport
	initErr      error
	statusReport brain.StatusReport
	statusErr    error
	importReport brain.TransferReport
	importErr    error
	importOpts   brain.ImportOptions
}

func (f *fakeBrainService) Init(context.Context) (brain.InitReport, error) {
	return f.initReport, f.initErr
}

func (f *fakeBrainService) Status(context.Context) (brain.StatusReport, error) {
	return f.statusReport, f.statusErr
}

func (f *fakeBrainService) Import(_ context.Context, opts brain.ImportOptions) (brain.TransferReport, error) {
	f.importOpts = opts
	return f.importReport, f.importErr
}

func initializedBrainReport() brain.StatusReport {
	return brain.StatusReport{
		State:             brain.StateInitialized,
		Path:              brain.Path,
		PathExists:        true,
		PathSecure:        true,
		NativeFilesystem:  true,
		FSType:            "ext4",
		Owner:             "hermes",
		Group:             "hermes",
		Mode:              "750",
		ManagedScaffold:   true,
		GitState:          brain.GitClean,
		MarkdownFiles:     3,
		AttachmentFiles:   0,
		TotalBytes:        4096,
		ProjectRegistered: true,
		SkillState:        brain.SkillNotInstalled,
		Issues:            []string{},
	}
}

func runBrainCLI(t *testing.T, args []string, service brainService) (int, string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	a := &app{
		stdout:   &stdout,
		stderr:   &stderr,
		build:    testBuild(),
		newBrain: func(*lima.Adapter, lima.BootstrapOptions) brainService { return service },
	}
	code := runWithApp(context.Background(), a, args)
	return code, stdout.String(), stderr.String()
}

func TestBrainNoSubcommandIsUsage(t *testing.T) {
	code, _, _ := runBrainCLI(t, []string{"brain"}, &fakeBrainService{})
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
}

func TestBrainCommandsWireLimaAdapterAndOperatorToBootstrapGate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	wantAdapter := &lima.Adapter{}
	service := &fakeBrainService{statusReport: initializedBrainReport()}
	var gotAdapter *lima.Adapter
	var gotOpts lima.BootstrapOptions
	a := &app{
		stdout:             &stdout,
		stderr:             &stderr,
		build:              testBuild(),
		newLima:            func() *lima.Adapter { return wantAdapter },
		lookupOperatorUser: func() (string, error) { return "testop", nil },
		newBrain: func(adapter *lima.Adapter, opts lima.BootstrapOptions) brainService {
			gotAdapter = adapter
			gotOpts = opts
			return service
		},
	}

	code := runWithApp(context.Background(), a, []string{"brain", "status", "--json"})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotAdapter != wantAdapter {
		t.Fatalf("Brain manager adapter = %p, want %p", gotAdapter, wantAdapter)
	}
	if gotOpts.OperatorUser != "testop" {
		t.Fatalf("bootstrap operator = %q, want testop", gotOpts.OperatorUser)
	}
}

func TestBrainInitJSONEnvelope(t *testing.T) {
	status := initializedBrainReport()
	service := &fakeBrainService{initReport: brain.InitReport{Created: true, Status: status}}

	code, stdout, stderr := runBrainCLI(t, []string{"brain", "init", "--json"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != true || env["command"] != "brain.init" {
		t.Fatalf("envelope = %#v", env)
	}
	data, _ := env["data"].(map[string]any)
	if data["state"] != "initialized" || data["created"] != true || data["path"] != brain.Path {
		t.Fatalf("data = %#v", data)
	}
	if data["project_registered"] != true || data["retrieval_skill"] != "not_installed" {
		t.Fatalf("registration/skill data = %#v", data)
	}
}

func TestBrainStatusJSONEnvelopeIncludesOnlyAggregates(t *testing.T) {
	status := initializedBrainReport()
	status.GitState = brain.GitDirty
	status.MarkdownFiles = 120
	status.AttachmentFiles = 7
	status.TotalBytes = 456789
	service := &fakeBrainService{statusReport: status}

	code, stdout, stderr := runBrainCLI(t, []string{"brain", "status", "--json"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	env := decodeOneEnvelope(t, stdout)
	data, _ := env["data"].(map[string]any)
	if data["markdown_files"] != float64(120) || data["attachment_files"] != float64(7) || data["total_bytes"] != float64(456789) {
		t.Fatalf("aggregate data = %#v", data)
	}
	if strings.Contains(stdout, "private-note-name") {
		t.Fatalf("status leaked a note name: %q", stdout)
	}
}

func TestBrainImportWiresOptionsAndEmitsBoundedJSON(t *testing.T) {
	service := &fakeBrainService{importReport: brain.TransferReport{
		DryRun:         true,
		Files:          2,
		Markdown:       1,
		Attachments:    1,
		Bytes:          42,
		ManifestSHA256: strings.Repeat("a", 64),
		Conflicts:      0,
		Skipped:        map[string]int{"excluded": 1},
		FinalPath:      brain.Path + "/archive",
	}}

	code, stdout, stderr := runBrainCLI(t, []string{
		"brain", "import", "/private/source", "--into", "archive", "--dry-run", "--json",
	}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if service.importOpts.Source != "/private/source" ||
		service.importOpts.Into != "archive" ||
		!service.importOpts.DryRun {
		t.Fatalf("import options = %#v", service.importOpts)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != true || env["command"] != "brain.import" {
		t.Fatalf("envelope = %#v", env)
	}
	data, _ := env["data"].(map[string]any)
	if data["files"] != float64(2) || data["manifest_sha256"] != strings.Repeat("a", 64) {
		t.Fatalf("transfer data = %#v", data)
	}
	if strings.Contains(stdout, "/private/source") {
		t.Fatalf("import output leaked the host source: %q", stdout)
	}
}

func TestBrainTransferErrorsDoNotEchoPrivateArguments(t *testing.T) {
	const marker = "private-customer-vault"
	service := &fakeBrainService{
		importErr: &brain.Error{Op: "import", Kind: brain.KindPrecondition, Err: errors.New("host source preflight failed")},
	}
	for _, args := range [][]string{
		{"brain", "import", "/tmp/" + marker, "--json"},
		{"brain", "import", "/tmp/" + marker, "--dry-run", "--json"},
	} {
		code, stdout, stderr := runBrainCLI(t, args, service)
		if code == int(ExitOK) {
			t.Fatalf("%v unexpectedly succeeded", args[:2])
		}
		if strings.Contains(stdout, marker) || strings.Contains(stderr, marker) {
			t.Fatalf("%v leaked a private argument: stdout=%q stderr=%q", args[:2], stdout, stderr)
		}
	}
}

// The human output distinguishes registered / not_registered / conflict, so the
// JSON envelope must too: a machine consumer cannot act on a slug conflict if it
// is indistinguishable from an unregistered project.
func TestBrainStatusJSONDistinguishesSlugConflictFromNotRegistered(t *testing.T) {
	cases := []struct {
		name           string
		mutate         func(*brain.StatusReport)
		wantRegistered bool
		wantConflict   bool
	}{
		{"registered", func(*brain.StatusReport) {}, true, false},
		{"not registered", func(r *brain.StatusReport) {
			r.ProjectRegistered = false
		}, false, false},
		{"slug conflict", func(r *brain.StatusReport) {
			r.ProjectRegistered = false
			r.ProjectConflict = true
			r.State = brain.StateDrift
			r.Issues = []string{"project_slug_conflict"}
		}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status := initializedBrainReport()
			tc.mutate(&status)
			service := &fakeBrainService{statusReport: status}

			code, stdout, stderr := runBrainCLI(t, []string{"brain", "status", "--json"}, service)
			if code != int(ExitOK) {
				t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
			}
			env := decodeOneEnvelope(t, stdout)
			data, _ := env["data"].(map[string]any)
			if data["project_registered"] != tc.wantRegistered || data["project_conflict"] != tc.wantConflict {
				t.Fatalf("project data = %#v, want registered=%t conflict=%t",
					data, tc.wantRegistered, tc.wantConflict)
			}
		})
	}
}

func TestBrainStatusHumanReportsDriftWithoutNoteNames(t *testing.T) {
	status := initializedBrainReport()
	status.State = brain.StateDrift
	status.Issues = []string{"canonical_scaffold_incomplete"}
	service := &fakeBrainService{statusReport: status}

	code, stdout, stderr := runBrainCLI(t, []string{"brain", "status"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "drift") || !strings.Contains(stdout, "canonical_scaffold_incomplete") {
		t.Fatalf("human status = %q", stdout)
	}
	if strings.Contains(stdout, "private-note-name") {
		t.Fatalf("human status leaked a note name: %q", stdout)
	}
}

func TestBrainInitConflictMapsToConflictExit(t *testing.T) {
	service := &fakeBrainService{
		initErr: &brain.Error{Op: "init", Kind: brain.KindConflict, Err: errors.New("unmanaged directory")},
	}
	code, stdout, _ := runBrainCLI(t, []string{"brain", "init", "--json"}, service)
	if code != int(ExitConflict) {
		t.Fatalf("exit = %d, want %d", code, ExitConflict)
	}
	env := decodeOneEnvelope(t, stdout)
	errObj, _ := env["error"].(map[string]any)
	if errObj["code"] != "CONFLICT" {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestMapBrainErrorExitCodes(t *testing.T) {
	cases := []struct {
		kind brain.ErrorKind
		exit ExitCode
	}{
		{brain.KindPrecondition, ExitPrecondition},
		{brain.KindConflict, ExitConflict},
		{brain.KindVerification, ExitVerification},
		{brain.KindGit, ExitExternal},
		{brain.KindRegistration, ExitExternal},
		{brain.KindGuestCommand, ExitExternal},
		{brain.KindTransport, ExitExternal},
		{brain.KindTimeout, ExitExternal},
		{brain.KindCancelled, ExitExternal},
	}
	for _, tc := range cases {
		got := mapBrainError("brain.status", &brain.Error{Op: "status", Kind: tc.kind})
		if got.Exit != tc.exit {
			t.Errorf("kind %s mapped to %d, want %d", tc.kind, got.Exit, tc.exit)
		}
	}
}

// Hermes keys its skill prompt cache on directories and toolsets, not on the
// file manifest, so a freshly written SKILL.md is invisible to every session
// already running. `brain init` must say that instead of implying the skill is
// live everywhere.
func TestBrainInitReportsRetrievalSkillActivation(t *testing.T) {
	status := initializedBrainReport()
	status.SkillState = brain.SkillInstalled
	service := &fakeBrainService{initReport: brain.InitReport{
		Created:      true,
		SkillUpdated: true,
		Status:       status,
	}}

	code, stdout, stderr := runBrainCLI(t, []string{"brain", "init"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, brain.SkillFilePath) {
		t.Errorf("human init output does not name the installed skill path: %q", stdout)
	}
	if !strings.Contains(stdout, "new session") {
		t.Errorf("human init output does not require a new session: %q", stdout)
	}
	if strings.Contains(stdout, "Task 13") {
		t.Errorf("human init output still defers the skill to Task 13: %q", stdout)
	}

	code, stdout, stderr = runBrainCLI(t, []string{"brain", "init", "--json"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	env := decodeOneEnvelope(t, stdout)
	data, _ := env["data"].(map[string]any)
	if data["retrieval_skill"] != "installed" || data["retrieval_skill_updated"] != true {
		t.Fatalf("skill data = %#v", data)
	}
}

// An unchanged payload must not tell the operator to restart anything, and a
// drifted one must point at the command that repairs it.
func TestBrainStatusReportsRetrievalSkillHonestly(t *testing.T) {
	cases := []struct {
		name      string
		state     brain.SkillState
		wantHuman string
	}{
		{"installed", brain.SkillInstalled, "cannot tell"},
		{"not installed", brain.SkillNotInstalled, "torio brain init"},
		{"drift", brain.SkillDrift, "torio brain init"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status := initializedBrainReport()
			status.SkillState = tc.state
			service := &fakeBrainService{statusReport: status}

			code, stdout, stderr := runBrainCLI(t, []string{"brain", "status"}, service)
			if code != int(ExitOK) {
				t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
			}
			if !strings.Contains(stdout, string(tc.state)) {
				t.Errorf("human status omits skill state %q: %q", tc.state, stdout)
			}
			if !strings.Contains(stdout, tc.wantHuman) {
				t.Errorf("human status omits %q: %q", tc.wantHuman, stdout)
			}

			code, stdout, stderr = runBrainCLI(t, []string{"brain", "status", "--json"}, service)
			if code != int(ExitOK) {
				t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
			}
			env := decodeOneEnvelope(t, stdout)
			data, _ := env["data"].(map[string]any)
			if data["retrieval_skill"] != string(tc.state) {
				t.Fatalf("retrieval_skill = %#v, want %q", data["retrieval_skill"], tc.state)
			}
		})
	}
}
