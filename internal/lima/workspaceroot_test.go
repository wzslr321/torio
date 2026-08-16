package lima

import (
	"strings"
	"testing"
)

// claudeWorkspace is a second backend's workspace, written literally rather than
// imported. internal/backend/claudecode is not reachable from here without a
// cycle, and the point of this test is a value that is not this package's own.
const claudeWorkspace = "/home/claude/projects"

// TestSharedHelpersNameNoBackendWorkspace is the regression for a bug that shipped:
// both shared helpers carried `/home/agent/projects`, so on any other backend the
// host derived the right path and the guest refused it. `project shell` and
// `project enter` were unusable on the second backend, and nothing failed until an
// operator tried to push.
//
// These two scripts serve every backend. A workspace written into them is the
// third backend's version of the same bug, so it is refused here rather than
// found again later.
func TestSharedHelpersNameNoBackendWorkspace(t *testing.T) {
	for name, content := range map[string][]byte{
		"operator shell": embeddedProjectShell,
		"project enter":  embeddedProjectEnter,
	} {
		script := string(content)
		if !strings.Contains(script, placeholderWorkspaceRoot) {
			t.Errorf("%s helper does not take a workspace placeholder", name)
		}
		for _, hardcoded := range []string{testWorkspacePath, claudeWorkspace} {
			if strings.Contains(script, hardcoded) {
				t.Errorf("%s helper names %q; a shared helper serves every backend", name, hardcoded)
			}
		}
	}
}

func TestProjectHelperSubstitutesTheDeclaredWorkspace(t *testing.T) {
	for name, root := range map[string]string{
		testUser: testWorkspacePath,
		"claude": claudeWorkspace,
	} {
		got, err := projectHelper(embeddedProjectShell, root, "operator shell")
		if err != nil {
			t.Fatalf("%s: projectHelper() error = %v", name, err)
		}
		script := string(got)
		if !strings.Contains(script, "workspace='"+root+"'") {
			t.Errorf("%s: resolved helper does not assign workspace=%q", name, root)
		}
		if strings.Contains(script, placeholderWorkspaceRoot) {
			t.Errorf("%s: resolved helper still carries the placeholder", name)
		}
	}
}

// TestProjectHelperRefusesAWorkspaceItCannotCarry: the root is a backend
// constant rather than operator input, and is still checked. It lands inside a
// single-quoted shell assignment, where a quote or a newline would end the
// assignment and start something else.
func TestProjectHelperRefusesAWorkspaceItCannotCarry(t *testing.T) {
	for name, root := range map[string]string{
		"empty":     "",
		"relative":  "home/claude/projects",
		"quote":     "/home/claude/projects'; rm -rf /; '",
		"newline":   "/home/claude/projects\nrm -rf /",
		"backslash": `/home/claude\projects`,
	} {
		if _, err := projectHelper(embeddedProjectShell, root, "operator shell"); err == nil {
			t.Errorf("%s: projectHelper() accepted %q", name, root)
		}
	}
}

// TestProjectHelperRefusesAScriptWithNoPlaceholder keeps the substitution
// mandatory. A helper that quietly shipped unsubstituted would refuse every
// project rather than the wrong ones, which is a worse failure than the one this
// replaced.
func TestProjectHelperRefusesAScriptWithNoPlaceholder(t *testing.T) {
	if _, err := projectHelper([]byte("#!/bin/bash\nworkspace='/home/agent/projects'\n"), testWorkspacePath, "operator shell"); err == nil {
		t.Error("projectHelper() accepted a script with nothing to substitute")
	}
}
