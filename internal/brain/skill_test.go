package brain

import (
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
			"staging":       m.stagingPath(),
			"skill staging": m.skillStagingPath(),
			"lock":          m.lockPath(),
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

// TestABackendWithNoSkillRootInstallsNothing pins the honest answer. Claude
// Code discovers skills, but the retrieval skill Torio ships is written for
// another backend's tools and vault path; installing it would tell the agent to
// call tools it does not have. "Not applicable" is the state, and it is
// deliberately distinct from "not installed".
func TestABackendWithNoSkillRootInstallsNothing(t *testing.T) {
	m := New(nil, lima.BootstrapOptions{Backend: claudecode.New()})
	if m.skillRoot() != "" {
		t.Fatalf("skillRoot = %q, want empty until a skill exists for this backend", m.skillRoot())
	}
	updated, err := m.installSkill(t.Context(), "init")
	if err != nil {
		t.Fatalf("installSkill: %v", err)
	}
	if updated {
		t.Error("installSkill reported an install for a backend with no skill root")
	}
	probe, err := m.probeSkill(t.Context(), "status", "", "")
	if err != nil {
		t.Fatalf("probeSkill: %v", err)
	}
	if probe.state != SkillNotApplicable {
		t.Errorf("skill state = %q, want %q", probe.state, SkillNotApplicable)
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
