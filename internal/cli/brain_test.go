/*
 * AI-Provenance:
 *   model: Cursor Grok 4.5
 *   harness: Cursor
 *   plugins:
 *     - lean-ai-provenance
 *   skills:
 *     - mark-ai-provenance
 */
package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/brain"
)

type fakeBrainService struct {
	initReport   brain.InitReport
	initErr      error
	statusReport brain.StatusReport
	statusErr    error
}

func (f *fakeBrainService) Init(context.Context) (brain.InitReport, error) {
	return f.initReport, f.initErr
}

func (f *fakeBrainService) Status(context.Context) (brain.StatusReport, error) {
	return f.statusReport, f.statusErr
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	a := &app{
		stdout:   &stdout,
		stderr:   &stderr,
		build:    testBuild(),
		newBrain: func() brainService { return service },
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
