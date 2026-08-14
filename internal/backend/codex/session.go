package codex

import "github.com/wzslr321/torio/internal/backend"

// Session is nil: no session helper is installed yet, and a declared session
// whose root-owned entry point does not exist would make bootstrap prove a file
// nothing put there.
func (codexBackend) Session() *backend.SessionSpec { return nil }

// BrainSkill is empty: nothing is installed into the skill root yet, so `brain
// status` reports no retrieval surface rather than reporting one as missing.
func (codexBackend) BrainSkill() backend.BrainSkill { return backend.BrainSkill{} }
