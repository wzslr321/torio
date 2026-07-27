/*
 * AI-Provenance:
 *   model: Cursor Grok 4.5
 *   harness: Cursor
 *   skills:
 *     - mark-ai-provenance
 */

package lima

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateOperatorUserAcceptsSafeNames(t *testing.T) {
	for _, name := range []string{"operator", "alice", "Bob_1", "a", "A_b-c"} {
		if err := validateOperatorUser(name); err != nil {
			t.Errorf("validateOperatorUser(%q) = %v, want nil", name, err)
		}
	}
}

// TestRenderTemplateInstallsTheOperatorShellHelper proves a freshly created V1
// VM already carries the guest side of `torio project shell`. Nothing else
// provisions that file, so without this the headline flow — shell in, push,
// exit — fails at the remote end on every new machine.
//
// The helper is provisioned as a data file rather than written by a script:
// Lima then owns the ownership and mode, and re-materializes the file root-owned
// on every boot. The session it opens carries the operator's forwarded agent, so
// nothing writable by the operator or by hermes may sit on that path.
func TestRenderTemplateInstallsTheOperatorShellHelper(t *testing.T) {
	body, err := renderTemplate(InitOptions{OperatorUser: "operator"})
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	text := string(body)

	for _, want := range []string{
		"- mode: data",
		"path: " + OperatorShellHelper,
		`owner: "root:root"`,
		`permissions: "0755"`,
		"overwrite: true",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered template is missing %q", want)
		}
	}

	// The provisioned bytes must be the same bytes the helper tests execute:
	// a template that shipped a paraphrase of the helper would be untested.
	block, ok := blockScalarAfter(text, "content: |")
	if !ok {
		t.Fatalf("rendered template has no content block for the helper")
	}
	if block != string(embeddedProjectShell) {
		t.Errorf("provisioned helper differs from the embedded helper:\n--- provisioned ---\n%s\n--- embedded ---\n%s", block, embeddedProjectShell)
	}
}

// TestRenderedTemplateValidatesWithInstalledLima checks the actual Lima 2.x
// schema when the pinned host dependency is available. CI jobs without Lima
// still exercise every byte and invariant above.
func TestRenderedTemplateValidatesWithInstalledLima(t *testing.T) {
	binary, err := exec.LookPath("limactl")
	if err != nil {
		t.Skip("limactl is not installed")
	}
	body, err := renderTemplate(InitOptions{OperatorUser: "operator"})
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	target := filepath.Join(t.TempDir(), "torio.yaml")
	if err := os.WriteFile(target, body, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	output, err := exec.Command(binary, "validate", target).CombinedOutput()
	if err != nil {
		t.Fatalf("limactl validate: %v: %s", err, output)
	}
}

// blockScalarAfter returns the YAML literal block scalar that follows the line
// containing marker, de-indented by the block's own indentation.
func blockScalarAfter(text, marker string) (string, bool) {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, marker) {
			start = i + 1
			break
		}
	}
	if start < 0 || start >= len(lines) {
		return "", false
	}
	indent := lines[start][:len(lines[start])-len(strings.TrimLeft(lines[start], " "))]
	if indent == "" {
		return "", false
	}
	var out []string
	for _, line := range lines[start:] {
		if line == "" {
			out = append(out, "")
			continue
		}
		if !strings.HasPrefix(line, indent) {
			break
		}
		out = append(out, strings.TrimPrefix(line, indent))
	}
	return strings.Join(out, "\n"), true
}

func TestValidateOperatorUserRejectsInjection(t *testing.T) {
	cases := []string{
		"",
		"  ",
		"$(id)",
		"`id`",
		"op er",
		"op;rm",
		"op|id",
		"op&id",
		"op/root",
		"op'x",
		"op\"x",
	}
	for _, name := range cases {
		if err := validateOperatorUser(name); err == nil {
			t.Errorf("validateOperatorUser(%q) = nil, want error", name)
		}
	}
}
