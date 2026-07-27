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
	"slices"
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
	if len(g.payloads()) != 5 {
		t.Fatalf("guest payload count = %d, want lock token plus 3 scaffold files plus the retrieval skill",
			len(g.payloads()))
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

// `show` exiting non-zero means the Hermes CLI is broken, not that the slug is
// absent. The list fallback then only proves the CLI is reachable; its output
// must never be parsed for the primary path.
func TestInitDoesNotTrustProjectListWhenShowIsUnavailable(t *testing.T) {
	g := initializedFake()
	g.showBrokenCLI = true

	report, err := New(g).Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if report.Created {
		t.Fatalf("report.Created = true, want idempotent verification")
	}
	if !g.saw("hermes project list") || !g.saw("hermes project create") {
		t.Fatalf("missing show result trusted ambiguous list output: %v", g.calls)
	}
}

func TestInitRefusesNonemptyUnmanagedDirectory(t *testing.T) {
	g := readyFake()
	g.empty = false

	_, err := New(g).Init(context.Background())
	assertKind(t, err, KindConflict)
	if g.saw("tee "+stagingPath) || g.saw("git -C "+stagingPath) || g.saw("hermes project create") {
		t.Fatalf("conflicting init mutated guest: %v", g.calls)
	}
}

func TestInitRefusesPartialScaffold(t *testing.T) {
	g := initializedFake()
	g.scaffold = false

	_, err := New(g).Init(context.Background())
	assertKind(t, err, KindConflict)
	if g.saw("tee "+stagingPath) || g.saw("mv -T "+stagingPath) {
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

func TestStatusRequiresBootstrapVerificationBeforeGuestWork(t *testing.T) {
	g := initializedFake()
	g.bootstrapErr = &lima.Error{
		Op:   "bootstrap",
		Kind: lima.KindVerificationFailed,
		Err:  errors.New("host mount present"),
	}

	_, err := New(g).Status(context.Background())
	assertKind(t, err, KindPrecondition)
	if len(g.calls) != 0 {
		t.Fatalf("status reached unverified guest: %v", g.calls)
	}
}

func TestInitRequiresBootstrapVerificationBeforeGuestWrites(t *testing.T) {
	g := readyFake()
	g.bootstrapErr = &lima.Error{
		Op:   "bootstrap",
		Kind: lima.KindVerificationFailed,
		Err:  errors.New("brain path ownership drift"),
	}

	_, err := New(g).Init(context.Background())
	assertKind(t, err, KindPrecondition)
	if len(g.calls) != 0 {
		t.Fatalf("init wrote to unverified guest: %v", g.calls)
	}
}

func TestStatusFailsClosedWhenPasswordlessSudoPreflightFails(t *testing.T) {
	g := readyFake()
	g.pathExists = false
	g.setFailure("sudo -n -- true", 1)

	report, err := New(g).Status(context.Background())
	assertKind(t, err, KindGuestCommand)
	if report.State == StateUninitialized {
		t.Fatalf("sudo failure was misreported as uninitialized: %#v", report)
	}
	if g.saw("test -d "+Path) || g.saw("test -L "+Path) {
		t.Fatalf("status interpreted path absence after failed sudo preflight: %v", g.calls)
	}
}

func TestStatusFailsClosedWhenRootPathProbeHasUnexpectedExit(t *testing.T) {
	g := readyFake()
	g.pathExists = false
	g.setFailure("test -d "+Path, 2)

	report, err := New(g).Status(context.Background())
	assertKind(t, err, KindGuestCommand)
	if report.State == StateUninitialized {
		t.Fatalf("path probe failure was misreported as uninitialized: %#v", report)
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

func TestStatusRejectsBrainPresentOnlyAsNonPrimaryProjectFolder(t *testing.T) {
	g := initializedFake()
	g.projectShow = "name: Second Brain\nprimary: /home/hermes/other\nfolders:\n  - " + Path + "\n"

	report, err := New(g).Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if report.ProjectRegistered {
		t.Fatalf("secondary folder was accepted as primary registration: %#v", report)
	}
	if !report.ProjectConflict || report.State != StateDrift {
		t.Fatalf("secondary-only path did not fail closed as drift: %#v", report)
	}
}

// Real `hermes project show <slug>` exits 0 for an unknown slug and prints
// nothing on stdout, so an absent project must never be reported as a slug
// conflict on an otherwise-fresh guest.
func TestStatusReportsUninitializedWhenProjectShowExitsZeroWithoutOutput(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fakeGuest)
	}{
		{"canonical path missing", func(g *fakeGuest) { g.pathExists = false }},
		{"canonical path empty", func(g *fakeGuest) { g.pathExists = true; g.empty = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := readyFake()
			tc.mutate(g)

			report, err := New(g).Status(context.Background())
			if err != nil {
				t.Fatalf("Status() error = %v", err)
			}
			if report.ProjectRegistered || report.ProjectConflict {
				t.Fatalf("absent project reported as registered/conflicting: %#v", report)
			}
			if report.State != StateUninitialized {
				t.Fatalf("state = %q, want %q; report=%#v", report.State, StateUninitialized, report)
			}
			for _, issue := range []string{"project_slug_conflict", "project_registered_without_scaffold"} {
				if slices.Contains(report.Issues, issue) {
					t.Fatalf("absent project raised issue %q: %#v", issue, report)
				}
			}
		})
	}
}

// An absent project must not make Init refuse a directory Torio just created
// empty; it must fall through to registration.
func TestInitRegistersProjectWhenProjectShowExitsZeroWithoutOutput(t *testing.T) {
	g := readyFake()
	g.pathExists = false

	report, err := New(g).Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !report.Created || report.Status.State != StateInitialized {
		t.Fatalf("report = %#v, want created initialized Brain", report)
	}
	if !g.saw("hermes project create Second Brain " + Path + " --slug " + ProjectSlug) {
		t.Fatalf("absent project was not registered: %v", g.calls)
	}
}

// The exit-code fix must not weaken conflict detection: a slug whose printed
// primary path is not ours is still a refusal.
func TestStatusReportsConflictWhenProjectSlugPointsElsewhere(t *testing.T) {
	g := initializedFake()
	g.registered = false
	g.wrongProject = true

	report, err := New(g).Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if report.ProjectRegistered || !report.ProjectConflict {
		t.Fatalf("foreign primary path was not a conflict: %#v", report)
	}
	if report.State != StateDrift {
		t.Fatalf("state = %q, want %q; report=%#v", report.State, StateDrift, report)
	}
	if !slices.Contains(report.Issues, "project_slug_conflict") {
		t.Fatalf("issues = %v, want project_slug_conflict", report.Issues)
	}
}

func TestInitRefusesConcurrentGuestLockBeforeStagingWork(t *testing.T) {
	g := readyFake()
	g.lockHeld = true

	_, err := New(g).Init(context.Background())
	assertKind(t, err, KindConflict)
	if g.saw("rm -rf -- "+stagingPath) || g.saw("tee "+stagingPath) || g.saw("mv -T "+stagingPath) {
		t.Fatalf("contending init touched shared staging: %v", g.calls)
	}
}

func TestInitGuestLockPreventsVerifyThenPromoteInterleaving(t *testing.T) {
	g := &blockingGuest{
		base:    readyFake(),
		blockOn: "hermes project show " + ProjectSlug,
		blocked: make(chan struct{}),
		unblock: make(chan struct{}),
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := New(g).Init(context.Background())
		firstDone <- err
	}()
	<-g.blocked

	_, secondErr := New(g).Init(context.Background())
	close(g.unblock)
	firstErr := <-firstDone

	assertKind(t, secondErr, KindConflict)
	if firstErr != nil {
		t.Fatalf("lock holder Init() error = %v", firstErr)
	}
}

func TestInitRecoversOwnedStaleGuestLock(t *testing.T) {
	g := readyFake()
	g.lockHeld = true
	g.lockStale = true

	report, err := New(g).Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !report.Created {
		t.Fatalf("report.Created = false, want recovered fresh init")
	}
	if !g.saw("mv -T " + lockPath) {
		t.Fatalf("stale lock was not atomically quarantined: %v", g.calls)
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

// The 60-character cap Hermes applies to a skill description is the whole
// always-on trigger budget: `extract_skill_description` truncates to
// `desc[:57] + "..."`, and the truncated string is the only text about this
// skill that reaches the system prompt of every session.
func TestRetrievalSkillPayloadContract(t *testing.T) {
	payload, digest, err := retrievalSkill()
	if err != nil {
		t.Fatalf("retrievalSkill() error = %v", err)
	}
	if len(digest) != 64 {
		t.Fatalf("digest = %q, want a hex sha256", digest)
	}
	text := string(payload)
	if !strings.HasPrefix(text, "---\n") {
		t.Fatalf("SKILL.md must start with YAML frontmatter, got %.20q", text)
	}
	front, _, ok := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	if !ok {
		t.Fatalf("SKILL.md frontmatter is not terminated")
	}
	if !strings.Contains(front, "name: "+SkillName) {
		t.Fatalf("frontmatter does not declare name %q: %q", SkillName, front)
	}
	description := skillDescription(front)
	if description == "" {
		t.Fatalf("frontmatter has no description: %q", front)
	}
	if len(description) > 60 {
		t.Fatalf("description is %d chars, want <= 60: %q", len(description), description)
	}
	// Hermes has no `when-to-use` and no `allowed-tools` frontmatter field;
	// declaring either would be an unenforced permission claim.
	for _, forbidden := range []string{"when-to-use", "allowed-tools"} {
		if strings.Contains(front, forbidden) {
			t.Errorf("frontmatter declares unsupported field %q", forbidden)
		}
	}
	if !strings.Contains(text, Path) {
		t.Errorf("skill body does not pin the canonical absolute path %q", Path)
	}
	if strings.Contains(text, "OBSIDIAN_VAULT_PATH") {
		t.Errorf("skill body depends on OBSIDIAN_VAULT_PATH; the canonical path is fixed")
	}
	// search_files and read_file are the only retrieval tools Hermes exposes;
	// there is no separate grep, glob, or ls tool to fall back on.
	for _, tool := range []string{"search_files", "read_file"} {
		if !strings.Contains(text, tool) {
			t.Errorf("skill body does not name the %s tool", tool)
		}
	}
}

func skillDescription(frontmatter string) string {
	for _, line := range strings.Split(frontmatter, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "description:")
		if !ok {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return ""
}

// The retrieval skill is what makes the Brain reachable from every project, so
// it must appear only once the Brain itself is promoted, committed and
// registered. A partial Brain must never become globally discoverable.
func TestInitInstallsRetrievalSkillOnlyAfterTheBrainFullySucceeds(t *testing.T) {
	g := readyFake()

	report, err := New(g).Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !report.SkillUpdated {
		t.Fatalf("report.SkillUpdated = false, want a first install")
	}
	if report.Status.SkillState != SkillInstalled {
		t.Fatalf("skill state = %q, want %q", report.Status.SkillState, SkillInstalled)
	}
	for _, fragment := range []string{
		"install -d -o hermes -g hermes -m 0750 " + SkillPath,
		"tee " + skillStagingPath,
		"chmod 0640 " + skillStagingPath,
		"mv -T " + skillStagingPath + " " + SkillFilePath,
		"sha256sum -- " + SkillFilePath,
	} {
		if !g.saw(fragment) {
			t.Errorf("missing typed guest argv containing %q", fragment)
		}
	}
	if g.saw(" sh ") || g.saw("sh -c") {
		t.Errorf("skill install used a forbidden shell: %v", g.calls)
	}
	promote := g.firstIndex("mv -T " + stagingPath + " " + Path)
	register := g.firstIndex("hermes project create")
	install := g.firstIndex("mv -T " + skillStagingPath)
	if promote < 0 || register < 0 || install < 0 || install < promote || install < register {
		t.Fatalf("skill promoted at call %d, want after scaffold promote=%d and register=%d",
			install, promote, register)
	}
}

// Every Init failure mode must leave the skill discovery root untouched. A
// globally advertised skill pointing at a half-built or unregistered vault
// would make every future session read a Brain Torio never verified.
func TestInitDoesNotInstallRetrievalSkillForAPartialBrain(t *testing.T) {
	cases := []struct {
		name   string
		guest  func() *fakeGuest
		mutate func(*fakeGuest)
		want   ErrorKind
	}{
		{"git commit failed", readyFake, func(g *fakeGuest) {
			g.setFailure("git -C "+stagingPath+" -c user.name=torio", 1)
		}, KindGit},
		{"project registration failed", readyFake, func(g *fakeGuest) {
			g.setFailure("hermes project create", 1)
		}, KindRegistration},
		{"unmanaged directory", readyFake, func(g *fakeGuest) {
			g.empty = false
		}, KindConflict},
		{"partial scaffold", initializedFake, func(g *fakeGuest) {
			g.scaffold = false
		}, KindConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.guest()
			tc.mutate(g)

			report, err := New(g).Init(context.Background())
			assertKind(t, err, tc.want)
			if report.SkillUpdated {
				t.Fatalf("report.SkillUpdated = true for a failed Init")
			}
			for _, fragment := range []string{
				"install -d -o hermes -g hermes -m 0750 " + SkillPath,
				"tee " + skillStagingPath,
				"mv -T " + skillStagingPath,
			} {
				if g.saw(fragment) {
					t.Errorf("failed Init wrote the retrieval skill: saw %q", fragment)
				}
			}
		})
	}
}

// Installation is content-addressed, so rerunning `brain init` against a
// current payload must not rewrite the file. A needless rewrite would tell the
// operator to restart sessions that are in fact already correct.
func TestInitLeavesACurrentRetrievalSkillUntouched(t *testing.T) {
	g := initializedFake().withInstalledSkill(t)

	report, err := New(g).Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if report.SkillUpdated {
		t.Fatalf("report.SkillUpdated = true, want no rewrite of a current payload")
	}
	if report.Status.SkillState != SkillInstalled {
		t.Fatalf("skill state = %q, want %q", report.Status.SkillState, SkillInstalled)
	}
	for _, fragment := range []string{
		"tee " + skillStagingPath,
		"mv -T " + skillStagingPath,
		"install -d -o hermes -g hermes -m 0750 " + SkillPath,
	} {
		if g.saw(fragment) {
			t.Errorf("idempotent init rewrote the retrieval skill: saw %q", fragment)
		}
	}
}

// Skill state is reported independently of the Brain's own state: a tampered or
// stale payload is drift the operator must see, but it is drift `brain init`
// repairs, so it must not make an otherwise healthy Brain unrecoverable.
func TestStatusReportsRetrievalSkillStateFromTheGuest(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*testing.T, *fakeGuest)
		want      SkillState
		wantIssue bool
	}{
		{"absent", func(*testing.T, *fakeGuest) {}, SkillNotInstalled, false},
		{"current payload", func(t *testing.T, g *fakeGuest) {
			g.withInstalledSkill(t)
		}, SkillInstalled, false},
		{"stale payload", func(t *testing.T, g *fakeGuest) {
			g.withInstalledSkill(t)
			g.skillDigest = strings.Repeat("0", 64)
		}, SkillDrift, true},
		{"world-readable payload", func(t *testing.T, g *fakeGuest) {
			g.withInstalledSkill(t)
			g.skillMode = "644"
		}, SkillDrift, true},
		{"foreign owner", func(t *testing.T, g *fakeGuest) {
			g.withInstalledSkill(t)
			g.skillOwner = "root"
		}, SkillDrift, true},
		{"symlinked payload", func(t *testing.T, g *fakeGuest) {
			g.withInstalledSkill(t)
			g.skillSymlink = true
		}, SkillDrift, true},
		{"empty skill directory", func(t *testing.T, g *fakeGuest) {
			g.skillDirMode = "750"
		}, SkillNotInstalled, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := initializedFake()
			tc.mutate(t, g)

			report, err := New(g).Status(context.Background())
			if err != nil {
				t.Fatalf("Status() error = %v", err)
			}
			if report.SkillState != tc.want {
				t.Fatalf("skill state = %q, want %q; report=%#v", report.SkillState, tc.want, report)
			}
			if got := slices.Contains(report.Issues, "retrieval_skill_drift"); got != tc.wantIssue {
				t.Fatalf("retrieval_skill_drift issue = %t, want %t; issues=%v", got, tc.wantIssue, report.Issues)
			}
			if report.State != StateInitialized {
				t.Fatalf("state = %q, want %q; skill drift must stay repairable", report.State, StateInitialized)
			}
		})
	}
}

// `brain init` is also the repair path: a stale or over-permissive payload is
// rewritten from the embedded template, and the operator is told a rewrite
// happened so they know open sessions are running the old text.
func TestInitRepairsADriftedRetrievalSkill(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fakeGuest)
	}{
		{"stale payload", func(g *fakeGuest) { g.skillDigest = strings.Repeat("0", 64) }},
		{"world-readable payload", func(g *fakeGuest) { g.skillMode = "644" }},
		{"over-permissive directory", func(g *fakeGuest) { g.skillDirMode = "755" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := initializedFake().withInstalledSkill(t)
			tc.mutate(g)

			report, err := New(g).Init(context.Background())
			if err != nil {
				t.Fatalf("Init() error = %v", err)
			}
			if !report.SkillUpdated || report.Status.SkillState != SkillInstalled {
				t.Fatalf("report = %#v, want a repaired retrieval skill", report)
			}
			_, digest, err := retrievalSkill()
			if err != nil {
				t.Fatalf("retrievalSkill() error = %v", err)
			}
			if g.skillDigest != digest || g.skillMode != "640" || g.skillDirMode != "750" {
				t.Fatalf("guest skill = %q %s dir %s, want the embedded payload at 640/750",
					g.skillDigest, g.skillMode, g.skillDirMode)
			}
		})
	}
}

// A symlinked skill path could redirect a root-owned write anywhere on the
// guest, so Torio refuses instead of following it.
func TestInitRefusesToWriteThroughASymlinkedSkillPath(t *testing.T) {
	for _, name := range []string{"payload", "directory"} {
		t.Run(name, func(t *testing.T) {
			g := initializedFake().withInstalledSkill(t)
			if name == "payload" {
				g.skillSymlink = true
			} else {
				g.skillDirSymlink = true
			}

			_, err := New(g).Init(context.Background())
			assertKind(t, err, KindConflict)
			if g.saw("tee "+skillStagingPath) || g.saw("mv -T "+skillStagingPath) {
				t.Fatalf("init wrote through a symlinked skill path: %v", g.calls)
			}
		})
	}
}

// The install path is the whole point of the task: only a skill under the
// global $HERMES_HOME/skills root is visible from every project. Pinning it
// here stops a later refactor from quietly moving it under the vault, under a
// project, or under the Hermes profile root itself.
func TestRetrievalSkillInstallsUnderTheGlobalHermesSkillsRoot(t *testing.T) {
	if SkillPath != lima.HermesProfilePath+"/skills/"+SkillName {
		t.Fatalf("SkillPath = %q, want $HERMES_HOME/skills/%s", SkillPath, SkillName)
	}
	if SkillFilePath != SkillPath+"/SKILL.md" {
		t.Fatalf("SkillFilePath = %q, want %s/SKILL.md", SkillFilePath, SkillPath)
	}
	if strings.HasPrefix(SkillPath, Path) {
		t.Fatalf("SkillPath %q is inside the vault; skills under the Brain are not discovered", SkillPath)
	}
	// Staging must not sit inside the discovery root: os.walk treats any
	// directory holding a SKILL.md as a skill, so a half-written payload there
	// would be loadable.
	if strings.HasPrefix(skillStagingPath, lima.HermesProfilePath+"/skills") {
		t.Fatalf("skillStagingPath %q is inside the skill discovery root", skillStagingPath)
	}
}
