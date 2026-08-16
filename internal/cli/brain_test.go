package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/backend/claudecode"
	"github.com/wzslr321/torio/internal/brain"
	"github.com/wzslr321/torio/internal/lima"
)

// TestBrainHelpNamesNoBoxAndNoGuestPath guards the one thing help text cannot
// know. It is built when the command tree is built, and `--help` short-circuits
// before the instance and the backend are resolved — so a baked-in instance
// name and vault path told a Claude Code operator to run
// `limactl copy torio:/home/claude/brain/`, naming the wrong box and the wrong
// directory in a single line that looks exactly right.
//
// The concrete command belongs in `brain status`, which knows what it just read.
func TestBrainHelpNamesNoBoxAndNoGuestPath(t *testing.T) {
	cmd := newBrainCmd(&app{})
	for _, c := range append([]*cobra.Command{cmd}, cmd.Commands()...) {
		for _, text := range []string{c.Short, c.Long} {
			for _, forbidden := range []string{"/home/", lima.InstanceName + ":"} {
				if strings.Contains(text, forbidden) {
					t.Errorf("%q help names %q, which is resolved per invocation:\n%s", c.Name(), forbidden, text)
				}
			}
		}
	}
}

// TestBrainStatusSaysWhereThisReplicaStands is the other half: the instruction
// did not disappear, it moved to where the values are known. What it says moved
// too. There is one Brain and this box holds a replica of it (ADR-0025), so the
// useful next step is reconciling, not the copy-out an operator used to compose
// by hand.
func TestBrainStatusSaysWhereThisReplicaStands(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report brain.StatusReport
		want   string
	}{
		{
			name:   "never reconciled",
			report: brain.StatusReport{State: brain.StateInitialized, Path: "/home/claude/brain"},
			want:   "torio brain sync",
		},
		{
			name: "level with the hub",
			report: brain.StatusReport{
				State: brain.StateInitialized, Path: "/home/claude/brain", HubRefKnown: true,
			},
			want: "level with the host vault",
		},
		{
			name: "out of step",
			report: brain.StatusReport{
				State: brain.StateInitialized, Path: "/home/claude/brain",
				HubRefKnown: true, AheadOfHub: 2, BehindHub: 1,
			},
			want: "torio brain sync",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeBrainService{statusReport: tc.report}
			var out bytes.Buffer
			a := &app{stdout: &out, stderr: &bytes.Buffer{}, build: testBuild()}
			a.newBrain = func(*lima.Adapter, lima.BootstrapOptions) brainService { return svc }
			a.newLima = func() *lima.Adapter { return nil }
			a.lookupOperatorUser = func() (string, error) { return "testop", nil }

			cmd := newBrainStatusCmd(a)
			cmd.SetContext(context.Background())
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatalf("brain status: %v", err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("status did not say %q\ngot: %s", tc.want, out.String())
			}
		})
	}
}

// The replica line is where an operator reads how far this box is from the
// rest, so it has to distinguish "never met the hub" from "level with it".
func TestBrainStatusDistinguishesNeverReconciledFromLevel(t *testing.T) {
	svc := &fakeBrainService{statusReport: brain.StatusReport{
		State: brain.StateInitialized, Path: "/home/claude/brain",
	}}
	var out bytes.Buffer
	a := &app{stdout: &out, stderr: &bytes.Buffer{}, build: testBuild()}
	a.newBrain = func(*lima.Adapter, lima.BootstrapOptions) brainService { return svc }
	a.newLima = func() *lima.Adapter { return nil }
	a.lookupOperatorUser = func() (string, error) { return "testop", nil }

	cmd := newBrainStatusCmd(a)
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("brain status: %v", err)
	}
	if !strings.Contains(out.String(), "never reconciled") {
		t.Errorf("status did not distinguish a box that never reconciled\ngot: %s", out.String())
	}
	if strings.Contains(out.String(), "0 ahead, 0 behind") {
		t.Errorf("a box that never reconciled was shown as level with the hub\ngot: %s", out.String())
	}
}

type fakeBrainService struct {
	initReport   brain.InitReport
	initErr      error
	statusReport brain.StatusReport
	statusErr    error
	importReport brain.TransferReport
	importErr    error
	importOpts   brain.ImportOptions
	syncReport   brain.SyncReport
	syncErr      error
	syncCalls    int
}

func (f *fakeBrainService) Init(context.Context) (brain.InitReport, error) {
	return f.initReport, f.initErr
}

func (f *fakeBrainService) Status(context.Context) (brain.StatusReport, error) {
	return f.statusReport, f.statusErr
}

func (f *fakeBrainService) Sync(context.Context) (brain.SyncReport, error) {
	f.syncCalls++
	return f.syncReport, f.syncErr
}

func (f *fakeBrainService) Import(_ context.Context, opts brain.ImportOptions) (brain.TransferReport, error) {
	f.importOpts = opts
	return f.importReport, f.importErr
}

func initializedBrainReport() brain.StatusReport {
	return brain.StatusReport{
		State:            brain.StateInitialized,
		Path:             claudecode.BrainPath,
		PathExists:       true,
		PathSecure:       true,
		NativeFilesystem: true,
		FSType:           "ext4",
		Owner:            claudecode.User,
		Group:            claudecode.User,
		Mode:             "750",
		ManagedScaffold:  true,
		GitState:         brain.GitClean,
		MarkdownFiles:    3,
		AttachmentFiles:  0,
		TotalBytes:       4096,
		SkillState:       brain.SkillNotInstalled,
		Issues:           []string{},
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
	if data["state"] != "initialized" || data["created"] != true || data["path"] != claudecode.BrainPath {
		t.Fatalf("data = %#v", data)
	}
	if data["retrieval_skill"] != "not_installed" {
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
		FinalPath:      claudecode.BrainPath + "/archive",
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

// A backend may key its skill prompt cache on directories and toolsets, not on the
// file manifest, so a freshly written SKILL.md is invisible to every session
// already running. `brain init` must say that instead of implying the skill is
// live everywhere.
func TestBrainInitReportsRetrievalSkillActivation(t *testing.T) {
	status := initializedBrainReport()
	status.SkillState = brain.SkillInstalled
	status.SkillPath = claudecode.ProfilePath + "/skills/" + brain.SkillName + "/SKILL.md"
	service := &fakeBrainService{initReport: brain.InitReport{
		Created:      true,
		SkillUpdated: true,
		Status:       status,
	}}

	code, stdout, stderr := runBrainCLI(t, []string{"brain", "init"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, claudecode.ProfilePath+"/skills/"+brain.SkillName+"/SKILL.md") {
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

// TestBrainSkillPathFollowsTheReport pins that the path an operator is told to
// look at is the one the guest actually has. It used to be a constant naming
// one backend's profile, which every box running another would have been handed
// with full confidence by the command whose only job is to report what is there.
func TestBrainSkillPathFollowsTheReport(t *testing.T) {
	const claudePath = "/home/claude/.claude/skills/torio-brain/SKILL.md"
	status := initializedBrainReport()
	status.SkillState = brain.SkillInstalled
	status.SkillPath = claudePath
	service := &fakeBrainService{statusReport: status}

	code, stdout, stderr := runBrainCLI(t, []string{"brain", "status"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, claudePath) {
		t.Errorf("status does not name the backend's own skill path: %q", stdout)
	}
	if strings.Contains(stdout, "/home/codex/") {
		t.Errorf("status names another backend's skill path: %q", stdout)
	}
}

// An unchanged payload must not tell the operator to restart anything, and a
// drifted one must point at the command that repairs it.
func TestBrainStatusReportsRetrievalSkillHonestly(t *testing.T) {
	cases := []struct {
		name      string
		state     brain.SkillState
		wantHuman string
		// avoidHuman is text that must not appear. It exists for the state whose
		// text and JSON used to disagree: a backend that declares no skill was
		// told in prose to run `torio brain init` to install one, while the
		// envelope beside it correctly said not_applicable.
		avoidHuman string
	}{
		{name: "installed", state: brain.SkillInstalled, wantHuman: "cannot tell"},
		{name: "not installed", state: brain.SkillNotInstalled, wantHuman: "torio brain init"},
		{name: "drift", state: brain.SkillDrift, wantHuman: "torio brain init"},
		{
			name:       "not applicable",
			state:      brain.SkillNotApplicable,
			wantHuman:  "no retrieval skill to install",
			avoidHuman: "torio brain init",
		},
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
			if tc.avoidHuman != "" && strings.Contains(stdout, tc.avoidHuman) {
				t.Errorf("human status contradicts the %q envelope by saying %q: %q", tc.state, tc.avoidHuman, stdout)
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

// One Brain, replicated into every backend's guest, means there has to be a way
// to make two boxes agree. The report is counts and one host path: what moved,
// never what it said (ADR-0025).
func TestBrainSyncReportsWhatMovedAndWhereTheHubIs(t *testing.T) {
	service := &fakeBrainService{
		syncReport: brain.SyncReport{
			HubPath:     "/home/op/.local/share/torio/brain/vault",
			Snapshotted: true,
			ToHub:       3,
			ToGuest:     2,
		},
	}

	code, stdout, stderr := runBrainCLI(t, []string{"brain", "sync"}, service)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if service.syncCalls != 1 {
		t.Errorf("sync ran %d times, want 1", service.syncCalls)
	}
	for _, want := range []string{"3", "2", "/home/op/.local/share/torio/brain/vault"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to carry %q", stdout, want)
		}
	}
}

// A conflict is an outcome an operator resolves with Git, not a failure of the
// command: the other direction may well have carried.
func TestBrainSyncReportsAConflictWithoutFailing(t *testing.T) {
	service := &fakeBrainService{
		syncReport: brain.SyncReport{
			HubPath:          "/home/op/.local/share/torio/brain/vault",
			ConflictOutbound: true,
			ToGuest:          1,
			Notes:            []string{"conflict_to_hub"},
		},
	}

	code, stdout, _ := runBrainCLI(t, []string{"brain", "sync"}, service)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0: a conflict is an outcome, not a failure", code)
	}
	if !strings.Contains(stdout, "conflict") {
		t.Errorf("stdout = %q, want it to name the conflict", stdout)
	}
	if !strings.Contains(stdout, "/home/op/.local/share/torio/brain/vault") {
		t.Errorf("stdout = %q, want it to say where the conflict is resolved", stdout)
	}
}

func TestBrainSyncEmitsOneEnvelope(t *testing.T) {
	service := &fakeBrainService{
		syncReport: brain.SyncReport{HubPath: "/home/op/vault", ToHub: 1},
	}

	code, stdout, _ := runBrainCLI(t, []string{"--json", "brain", "sync"}, service)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			HubPath string `json:"hub_path"`
			ToHub   int    `json:"commits_to_hub"`
			ToGuest int    `json:"commits_to_guest"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not one envelope: %v; got %q", err, stdout)
	}
	if !env.OK || env.Data.ToHub != 1 || env.Data.HubPath != "/home/op/vault" {
		t.Errorf("envelope = %+v, want the sync report", env)
	}
}
