package brain

import "github.com/wzslr321/torio/internal/backend/claudecode"

// The guest paths the manager derives for the fixture backend.
//
// They are spelled out here rather than imported from the package under test on
// purpose: the manager derives every one of them from the backend identity, and
// a test that reused the same derivation would pass whatever that derivation
// did. Writing them down is what makes a move visible.
const (
	// Path is the fixture backend's Second Brain vault.
	Path = claudecode.BrainPath

	stagingPath      = claudecode.Home + "/.torio-brain-staging"
	skillStagingPath = claudecode.Home + "/.torio-brain-skill-staging"
	lockPath         = claudecode.Home + "/.torio-brain-init.lock"
	syncStagingPath  = claudecode.Home + "/.torio-brain-sync-staging"

	// skillRoot is where the fixture backend discovers skills. It declares no
	// category, so the skill sits directly under the root.
	skillRoot     = claudecode.ProfilePath + "/skills"
	skillDir      = skillRoot + "/" + SkillName
	skillFilePath = skillDir + "/SKILL.md"
)
