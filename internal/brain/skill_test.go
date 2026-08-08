package brain

import (
	"context"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/backend/claudecode"
	"github.com/wzslr321/torio/internal/lima"
)

// TestTheVaultFollowsTheBackendIdentity pins that a second backend keeps its
// vault, staging and lock in its own home. A staging directory that landed in
// another identity's home would be a directory the owning identity cannot
// write, which is exactly how the import transfer failed once before.
func TestTheVaultFollowsTheBackendIdentity(t *testing.T) {
	for _, b := range []backend.Backend{lima.Hermes(), claudecode.New()} {
		m := New(nil, lima.BootstrapOptions{Backend: b})
		id := b.Identity()

		if m.vault() != id.BrainPath {
			t.Errorf("%s: vault = %q, want %q", id.Name, m.vault(), id.BrainPath)
		}
		for name, got := range map[string]string{
			"staging":          m.stagingPath(),
			"skill staging":    m.skillStagingPath(),
			"lock":             m.lockPath(),
			"import staging":   m.importStagingPath(),
			"import payload":   m.importPayloadPath(),
			"import manifest":  m.importManifestPath(),
			"import candidate": m.importCandidatePath(),
		} {
			if got == "" || got[:len(id.Home)] != id.Home {
				t.Errorf("%s: %s path %q is not under the identity's home %q", id.Name, name, got, id.Home)
			}
		}
		if m.agentUser() != id.GuestUser {
			t.Errorf("%s: agent user = %q, want %q", id.Name, m.agentUser(), id.GuestUser)
		}
	}
}

// skilllessBackend declares no retrieval skill. No shipped backend does any
// more, and the contract still admits one — so the honest answer stays pinned
// by a test rather than by the accident that nothing exercises it. Only the two
// methods the skill paths read are overridden; anything else this reached would
// panic, which is the intended way to find out it reached further than it says.
type skilllessBackend struct{ backend.Backend }

func (skilllessBackend) Identity() backend.Identity     { return claudecode.New().Identity() }
func (skilllessBackend) BrainSkill() backend.BrainSkill { return backend.BrainSkill{} }

func TestActivateRetrievalPreservesNotApplicableSkillState(t *testing.T) {
	m := New(nil, lima.BootstrapOptions{Backend: skilllessBackend{}})
	report := InitReport{Status: StatusReport{State: StateInitialized, SkillState: SkillNotApplicable}}

	if err := m.activateRetrieval(t.Context(), "init", &report); err != nil {
		t.Fatalf("activateRetrieval: %v", err)
	}
	if report.SkillUpdated {
		t.Error("a backend with no skill reported an update")
	}
	if report.Status.SkillState != SkillNotApplicable {
		t.Errorf("skill state = %q, want %q", report.Status.SkillState, SkillNotApplicable)
	}
}

type testProjectRegistry struct{ created bool }

func (r *testProjectRegistry) Status(_ context.Context, _ backend.Transport, _ string, _ string) (backend.RegistryStatus, error) {
	return backend.RegistryStatus{Present: r.created, PrimaryMatches: r.created}, nil
}

func (r *testProjectRegistry) Create(_ context.Context, _ backend.Transport, _ string, _ string, _ string) error {
	r.created = true
	return nil
}

func (*testProjectRegistry) Restore(context.Context, backend.Transport, string) error  { return nil }
func (*testProjectRegistry) Archive(context.Context, backend.Transport, string) error  { return nil }
func (*testProjectRegistry) Activate(context.Context, backend.Transport, string) error { return nil }

type registryBackend struct {
	backend.Backend
	registry backend.ProjectRegistry
}

func (b registryBackend) Registry() backend.ProjectRegistry { return b.registry }

func TestInitUsesTheDeclaredProjectRegistry(t *testing.T) {
	g := readyFake()
	registry := &testProjectRegistry{}
	b := registryBackend{Backend: lima.Hermes(), registry: registry}

	if _, err := New(g, lima.BootstrapOptions{Backend: b}).Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !registry.created {
		t.Fatal("Brain did not create its project through the backend registry")
	}
	if g.saw("hermes project") {
		t.Fatalf("Brain bypassed the backend registry: %v", g.calls)
	}
}

// TestABackendWithNoSkillInstallsNothing pins the honest answer: "not
// applicable" is a state, and it is deliberately distinct from "not installed".
// Reporting a missing thing where nothing is missing is how an operator learns
// to ignore the report that matters.
func TestABackendWithNoSkillInstallsNothing(t *testing.T) {
	m := New(nil, lima.BootstrapOptions{Backend: skilllessBackend{}})
	updated, err := m.installSkill(t.Context(), "init")
	if err != nil {
		t.Fatalf("installSkill: %v", err)
	}
	if updated {
		t.Error("installSkill reported an install for a backend that declares no skill")
	}
	probe, err := m.probeSkill(t.Context(), "status", "", "")
	if err != nil {
		t.Fatalf("probeSkill: %v", err)
	}
	if probe.state != SkillNotApplicable {
		t.Errorf("skill state = %q, want %q", probe.state, SkillNotApplicable)
	}
	if got := m.newStatusReport(); got.SkillState != SkillNotApplicable || got.SkillPath != "" {
		t.Errorf("status report = %q at %q, want %q at no path", got.SkillState, got.SkillPath, SkillNotApplicable)
	}
}

// TestTheClaudeSkillIsWrittenForClaude is the check that keeps this from being
// plumbing that installs the wrong document. The payload has to name the tools
// this agent has and the vault its own identity owns; a copy of the other
// backend's skill would install cleanly, verify green, and tell the agent to
// call tools that do not exist against a directory that does not exist.
func TestTheClaudeSkillIsWrittenForClaude(t *testing.T) {
	b := claudecode.New()
	m := New(nil, lima.BootstrapOptions{Backend: b})
	skill := b.BrainSkill()

	if !skill.Installable() {
		t.Fatal("the Claude Code backend declares no installable retrieval skill")
	}
	if got, want := m.skillFilePath(), "/home/claude/.claude/skills/"+SkillName+"/SKILL.md"; got != want {
		t.Errorf("skill file = %q, want %q", got, want)
	}
	// No category, and therefore no category description and no pre-category
	// path. Both exist on the other backend to win a position in a static
	// alphabetical index; Claude Code routes by reading descriptions instead.
	if m.skillCategoryFilePath() != "" || m.legacySkillPath() != "" {
		t.Errorf("category description %q and legacy path %q should both be empty for an uncategorized backend",
			m.skillCategoryFilePath(), m.legacySkillPath())
	}

	text := string(skill.Payload)
	if !strings.HasPrefix(text, "---\nname: "+SkillName+"\n") {
		t.Errorf("the skill does not open with frontmatter naming it %q; Claude Code matches the name to its directory", SkillName)
	}
	if !strings.Contains(text, b.Identity().BrainPath) {
		t.Errorf("the skill never names the vault it is for, %q", b.Identity().BrainPath)
	}
	// The specific ways this would be the other backend's document.
	for _, foreign := range []string{"/home/hermes", "search_files", "read_file", "skill_view"} {
		if strings.Contains(text, foreign) {
			t.Errorf("the Claude skill names %q, which belongs to another backend", foreign)
		}
	}
	for _, tool := range []string{"Grep", "Glob", "Read"} {
		if !strings.Contains(text, tool) {
			t.Errorf("the skill never names the %s tool the agent would retrieve with", tool)
		}
	}
}

// TestTheHermesSkillLayoutIsUnchanged guards the backend that has one: the
// category grouping is load-bearing for its skill index, and losing it would
// quietly demote the skill to the bottom of an alphabetical list.
func TestTheHermesSkillLayoutIsUnchanged(t *testing.T) {
	m := New(nil, lima.BootstrapOptions{Backend: lima.Hermes()})
	if got, want := m.skillCategoryPath(), lima.HermesProfilePath+"/skills/brain"; got != want {
		t.Errorf("category path = %q, want %q", got, want)
	}
	if got, want := m.skillFilePath(), lima.HermesProfilePath+"/skills/brain/"+SkillName+"/SKILL.md"; got != want {
		t.Errorf("skill file = %q, want %q", got, want)
	}
	if m.skillCategoryFilePath() == "" {
		t.Error("the category description path is empty; the uncapped index line is how the rule reaches every session")
	}
	if m.legacySkillPath() == "" {
		t.Error("the pre-category path is empty; a stale copy there makes skill lookup ambiguous and refuse outright")
	}
}
