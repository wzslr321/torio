package lima

import "testing"

func TestHermesProjectListedMatchesTheSlugColumnOnly(t *testing.T) {
	const listing = "  demo                     Demo  [1 folder(s)]\n" +
		"  other                    demo  [2 folder(s)]\n"

	if !hermesProjectListed(listing, "demo") {
		t.Error("the listed slug was not found")
	}
	if hermesProjectListed(listing, "absent") {
		t.Error("an absent slug was reported as listed")
	}
	// "demo" is the *name* of the second project. A substring search would let
	// one project answer an existence question about another.
	if hermesProjectListed("  other                    demo  [2 folder(s)]\n", "demo") {
		t.Error("a project name answered for a different project's slug")
	}
	if hermesProjectListed("No projects yet. Create one with `hermes project create <name>`.\n", "demo") {
		t.Error("the empty-listing sentence was read as a project")
	}
}
