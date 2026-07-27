/*
 * AI-Provenance:
 *   model: Cursor Grok 4.5
 *   harness: Cursor
 *   plugins:
 *     - lean-ai-provenance
 *   skills:
 *     - mark-ai-provenance
 */
package brain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/lima"
)

func TestInitFreshBrainUsesStagingCommitAndRegistersProject(t *testing.T) {
	g := readyFake()

	report, err := New(g).Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !report.Created || report.Status.State != StateInitialized {
		t.Fatalf("report = %#v, want created initialized Brain", report)
	}
	for _, fragment := range []string{
		"install -d -o hermes -g hermes -m 0750 " + stagingPath,
		"git -C " + stagingPath + " init",
		"git -C " + stagingPath + " add -- README.md AGENTS.md todo.md",
		"git -C " + stagingPath + " -c user.name=torio -c user.email=torio@localhost commit",
		"mv -T " + stagingPath + " " + Path,
		"hermes project create Second Brain " + Path + " --slug " + ProjectSlug,
	} {
		if !g.saw(fragment) {
			t.Errorf("missing typed guest argv containing %q", fragment)
		}
	}
	if g.saw(" sh ") || g.saw("sh -c") || g.saw("--use") {
		t.Errorf("init used a forbidden shell or --use: %v", g.calls)
	}
	if len(g.payloads()) != 3 {
		t.Fatalf("scaffold payload count = %d, want 3", len(g.payloads()))
	}
}

func TestInitIsIdempotentAndDoesNotDuplicateProject(t *testing.T) {
	g := initializedFake()

	report, err := New(g).Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if report.Created || report.Status.State != StateInitialized {
		t.Fatalf("report = %#v, want unchanged initialized Brain", report)
	}
	if g.saw("hermes project create") || g.saw("git -C "+stagingPath+" init") {
		t.Fatalf("idempotent init mutated existing Brain: %v", g.calls)
	}
}

func TestInitCreatesMissingCanonicalDirectoryBeforeScaffolding(t *testing.T) {
	g := readyFake()
	g.pathExists = false

	report, err := New(g).Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !report.Created {
		t.Fatalf("report.Created = false, want true")
	}
	if !g.saw("install -d -o hermes -g hermes -m 0750 " + Path) {
		t.Fatalf("missing canonical private-directory creation: %v", g.calls)
	}
}

func TestInitUsesMatchingProjectListWithoutDuplicateCreate(t *testing.T) {
	g := initializedFake()
	g.showMissing = true

	report, err := New(g).Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if report.Created {
		t.Fatalf("report.Created = true, want idempotent verification")
	}
	if !g.saw("hermes project list") || g.saw("hermes project create") {
		t.Fatalf("matching primary path was not reused: %v", g.calls)
	}
}

func TestInitRefusesNonemptyUnmanagedDirectory(t *testing.T) {
	g := readyFake()
	g.empty = false

	_, err := New(g).Init(context.Background())
	assertKind(t, err, KindConflict)
	if g.saw("tee ") || g.saw("git -C "+stagingPath) || g.saw("hermes project create") {
		t.Fatalf("conflicting init mutated guest: %v", g.calls)
	}
}

func TestInitRefusesPartialScaffold(t *testing.T) {
	g := initializedFake()
	g.scaffold = false

	_, err := New(g).Init(context.Background())
	assertKind(t, err, KindConflict)
	if g.saw("tee ") || g.saw("mv -T "+stagingPath) {
		t.Fatalf("partial scaffold was overwritten: %v", g.calls)
	}
}

func TestStatusReportsDriftForPathAndPartialScaffold(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fakeGuest)
	}{
		{"wrong owner", func(g *fakeGuest) { g.owner = "root" }},
		{"wrong mode", func(g *fakeGuest) { g.mode = "755" }},
		{"wrong fstype", func(g *fakeGuest) { g.fstype = "virtiofs" }},
		{"partial scaffold", func(g *fakeGuest) { g.scaffold = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := initializedFake()
			tc.mutate(g)
			report, err := New(g).Status(context.Background())
			if err != nil {
				t.Fatalf("Status() error = %v", err)
			}
			if report.State != StateDrift {
				t.Fatalf("state = %q, want drift; report=%#v", report.State, report)
			}
		})
	}
}

func TestInitSurfacesGitFailuresBeforePromotion(t *testing.T) {
	for _, fragment := range []string{
		"git -C " + stagingPath + " init",
		"git -C " + stagingPath + " -c user.name=torio",
	} {
		t.Run(fragment, func(t *testing.T) {
			g := readyFake()
			g.setFailure(fragment, 1)
			_, err := New(g).Init(context.Background())
			assertKind(t, err, KindGit)
			if g.saw("mv -T "+stagingPath) || g.saw("hermes project create") {
				t.Fatalf("failed Git setup was promoted or registered: %v", g.calls)
			}
			if !g.saw("rm -rf -- " + stagingPath) {
				t.Fatalf("failed staging was not cleaned up: %v", g.calls)
			}
		})
	}
}

func TestInitSurfacesHermesRegistrationFailureAfterSafePromotion(t *testing.T) {
	g := readyFake()
	g.setFailure("hermes project create", 1)

	report, err := New(g).Init(context.Background())
	assertKind(t, err, KindRegistration)
	if !report.Created {
		t.Fatalf("report.Created = false, want true after promoted scaffold")
	}
	if !g.saw("mv -T " + stagingPath) {
		t.Fatalf("registration was attempted before promotion: %v", g.calls)
	}
}

func TestStatusIsBoundedAggregateOnlyAndRedactsNames(t *testing.T) {
	g := initializedFake()
	g.gitDirty = true
	g.setCounts(1234, 56, 987654)

	report, err := New(g).Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if report.MarkdownFiles != 1234 || report.AttachmentFiles != 56 || report.TotalBytes != 987654 {
		t.Fatalf("counts = %#v", report)
	}
	if report.GitState != GitDirty || report.SkillState != SkillNotInstalled {
		t.Fatalf("git/skill state = %q/%q", report.GitState, report.SkillState)
	}
	rendered := strings.Join([]string{
		string(report.State), report.Path, report.FSType, report.Owner, report.Group,
		report.Mode, string(report.GitState), string(report.SkillState),
	}, " ")
	if strings.Contains(rendered, "private-note-name") {
		t.Fatalf("status leaked a note name: %q", rendered)
	}
}

func TestStatusFailsClosedOnTruncatedAggregateWithoutLeakingPayload(t *testing.T) {
	g := initializedFake()
	g.truncateOn = "find " + Path + " -type f -name *.md"

	_, err := New(g).Status(context.Background())
	assertKind(t, err, KindVerification)
	if strings.Contains(err.Error(), "private-note-name") {
		t.Fatalf("truncation error leaked a note name: %v", err)
	}
}

func TestStatusRequiresRunningVM(t *testing.T) {
	g := initializedFake()
	g.state = lima.StateStopped

	_, err := New(g).Status(context.Background())
	assertKind(t, err, KindPrecondition)
	if len(g.calls) != 0 {
		t.Fatalf("status reached stopped guest: %v", g.calls)
	}
}

func TestInitRejectsWrongExistingProjectWithoutCreatingDuplicate(t *testing.T) {
	g := initializedFake()
	g.registered = false
	g.wrongProject = true

	_, err := New(g).Init(context.Background())
	assertKind(t, err, KindRegistration)
	if g.saw("hermes project create") {
		t.Fatalf("init duplicated a conflicting slug: %v", g.calls)
	}
}

func assertKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want kind %q", want)
	}
	var got *Error
	if !errors.As(err, &got) {
		t.Fatalf("error type = %T, want *brain.Error: %v", err, err)
	}
	if got.Kind != want {
		t.Fatalf("error kind = %q, want %q: %v", got.Kind, want, err)
	}
}
