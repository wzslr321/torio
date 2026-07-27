package lima

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// helperScript writes the embedded guest helper to a temp file and returns the
// path, so the shipped artifact itself — not a copy of it — is what the tests
// execute.
func helperScript(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available; the guest helper cannot be executed here")
	}
	path := filepath.Join(t.TempDir(), "torio-project-shell")
	if err := os.WriteFile(path, embeddedProjectShell, 0o755); err != nil {
		t.Fatalf("writing the guest helper: %v", err)
	}
	return path
}

// runHelper runs the guest helper with args and returns its exit code, stdout
// and stderr.
func runHelper(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	cmd := exec.Command(helperScript(t), args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit int
	switch e := err.(type) {
	case nil:
	case *exec.ExitError:
		exit = e.ExitCode()
	default:
		t.Fatalf("running the guest helper: %v", err)
	}
	return exit, stdout.String(), stderr.String()
}

// TestProjectShellHelperRejectsMalformedArguments proves the guest validates
// the project path itself. The host validates it too
// (internal/lima.validateProjectPath), but the host is a caller, not a trusted
// input source: a helper that trusted its argument would turn one compromised
// or buggy caller into an arbitrary directory — or an ssh option — inside a
// session that carries the operator's forwarded agent.
func TestProjectShellHelperRejectsMalformedArguments(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no arguments", nil},
		{"two arguments", []string{HermesWorkspacePath + "/demo", HermesWorkspacePath + "/other"}},
		{"empty argument", []string{""}},
		{"relative path", []string{"demo"}},
		{"outside the workspace", []string{"/etc"}},
		{"the workspace root itself", []string{HermesWorkspacePath}},
		{"workspace root with a slash", []string{HermesWorkspacePath + "/"}},
		{"trailing slash", []string{HermesWorkspacePath + "/demo/"}},
		{"nested below a project", []string{HermesWorkspacePath + "/demo/src"}},
		{"parent traversal", []string{HermesWorkspacePath + "/../.ssh"}},
		{"dot segment", []string{HermesWorkspacePath + "/./demo"}},
		{"flag-shaped id", []string{HermesWorkspacePath + "/-oProxyCommand=touch /tmp/pwned"}},
		{"space in id", []string{HermesWorkspacePath + "/de mo"}},
		{"shell metacharacters", []string{HermesWorkspacePath + "/demo;id"}},
		{"command substitution", []string{HermesWorkspacePath + "/demo$(id)"}},
		{"newline", []string{HermesWorkspacePath + "/demo\nid"}},
		{"quote", []string{HermesWorkspacePath + "/de'mo"}},
		{"overlong id", []string{HermesWorkspacePath + "/" + strings.Repeat("a", 65)}},
		{"absent project", []string{HermesWorkspacePath + "/definitely-not-a-project"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runHelper(t, tc.args...)
			if exit == 0 {
				t.Fatalf("exit = 0, want a refusal (stdout %q, stderr %q)", stdout, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing on the operator's stdout for a refusal", stdout)
			}
			if !strings.HasPrefix(stderr, "torio-project-shell: ") {
				t.Errorf("stderr = %q, want a named refusal", stderr)
			}
		})
	}
}

// TestProjectShellHelperNeverEchoesTheRejectedArgument proves a refusal message
// carries no attacker-shaped bytes. The argument is unvalidated at the point it
// is rejected, so echoing it would write terminal escape sequences straight
// into the operator's terminal.
func TestProjectShellHelperNeverEchoesTheRejectedArgument(t *testing.T) {
	const marker = "MARKERVALUE"
	_, _, stderr := runHelper(t, HermesWorkspacePath+"/"+marker+";id")
	if strings.Contains(stderr, marker) {
		t.Errorf("stderr = %q, want it to name the rule, not the rejected value", stderr)
	}
}

// TestProjectShellHelperIsValidBash proves the shipped bytes parse as bash. The
// helper is written once into a VM image and only ever runs on a guest, so a
// syntax error would first surface as a broken `torio project shell` on a real
// machine.
func TestProjectShellHelperIsValidBash(t *testing.T) {
	path := helperScript(t)

	out, err := exec.Command("bash", "-n", path).CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n: %v: %s", err, out)
	}
	if !strings.HasPrefix(string(embeddedProjectShell), "#!/bin/bash\n") {
		t.Errorf("helper does not start with a bash shebang; sg's -c runs through sh")
	}
}

// TestProjectShellHelperNeverDumpsTheSessionEnvironment proves the helper never
// reads or prints the environment it is trusted to pass through. That
// environment carries SSH_AUTH_SOCK: the session's whole write capability, and
// the one value ADR-0015 keeps out of Torio's hands.
func TestProjectShellHelperNeverDumpsTheSessionEnvironment(t *testing.T) {
	code := helperCode(t)
	for _, forbidden := range []string{"SSH_AUTH_SOCK", "env", "printenv", "declare -p", "export -p", "ssh-add", "set -x", "-eux", "xtrace"} {
		if line := lineContaining(code, forbidden); line != "" {
			t.Errorf("helper line %q uses %q; the session environment must pass through untouched and untraced", line, forbidden)
		}
	}
}

// TestProjectShellHelperEntersTheGroupWithoutPrivilege proves the session is the
// operator's own identity under the shared project group — the promoted spike's
// shape — and never a privileged one. sudo or su here would hand the forwarded
// agent to root and leave root-owned files in a checkout hermes has to keep
// working in.
func TestProjectShellHelperEntersTheGroupWithoutPrivilege(t *testing.T) {
	code := helperCode(t)
	for _, forbidden := range []string{"sudo", "su -", "newgrp", "runuser"} {
		if line := lineContaining(code, forbidden); line != "" {
			t.Errorf("helper line %q uses %q; the operator session must not gain privilege", line, forbidden)
		}
	}
	if !strings.Contains(code, torioProjectsGroup) {
		t.Errorf("helper does not name the shared project group %q", torioProjectsGroup)
	}

	// sg's -c argument is the one string on the guest side that could carry a
	// command. It has to stay a constant: no expansion, no substitution, nothing
	// derived from the caller's argument.
	sg := lineContaining(code, "exec sg ")
	if sg == "" {
		t.Fatalf("helper never enters the project group with sg")
	}
	command := betweenSingleQuotes(sg)
	if command == "" {
		t.Fatalf("sg line %q does not pass a single-quoted constant command", sg)
	}
	if strings.ContainsAny(command, "$`\"") {
		t.Errorf("sg command %q is not a constant: it expands something", command)
	}
	if !strings.Contains(command, "bash") {
		t.Errorf("sg command %q does not force bash; sg -c runs through sh", command)
	}
}

// TestProjectShellHelperMarksTheSessionPrompt proves the session is
// distinguishable from the operator's own terminal at a glance: the prompt names
// the project, and the shell that renders it cannot be reconfigured by a guest
// ~/.bashrc.
func TestProjectShellHelperMarksTheSessionPrompt(t *testing.T) {
	code := helperCode(t)
	ps1 := lineContaining(code, "PS1=")
	if ps1 == "" {
		t.Fatalf("helper never sets a prompt")
	}
	if !strings.Contains(ps1, "export ") {
		t.Errorf("prompt line %q does not export PS1; the session shell would not inherit it", ps1)
	}
	if !strings.Contains(ps1, "torio:") || !strings.Contains(ps1, "project_id") {
		t.Errorf("prompt line %q does not read torio:<project-id>", ps1)
	}
	if !strings.Contains(code, "--norc") {
		t.Errorf("session shell does not use --norc; a guest ~/.bashrc would overwrite the prompt")
	}
}

// helperCode is the embedded helper with comments and blank lines removed, so a
// guard asserts what the script does and not what its prose says.
func helperCode(t *testing.T) string {
	t.Helper()

	var code []string
	for _, line := range strings.Split(string(embeddedProjectShell), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		code = append(code, trimmed)
	}
	if len(code) == 0 {
		t.Fatalf("embedded helper has no code lines")
	}
	return strings.Join(code, "\n")
}

// lineContaining returns the first line of code that contains want, or "" when
// no line matches.
func lineContaining(code, want string) string {
	for _, line := range strings.Split(code, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

// betweenSingleQuotes returns the text between the first and last single quote
// of line, or "" when there is no quoted span.
func betweenSingleQuotes(line string) string {
	first := strings.Index(line, "'")
	last := strings.LastIndex(line, "'")
	if first < 0 || last <= first {
		return ""
	}
	return line[first+1 : last]
}
