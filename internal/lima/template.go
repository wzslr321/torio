/*
 * AI-Provenance:
 *   model: Cursor Grok 4.5
 *   harness: Cursor
 *   skills:
 *     - mark-ai-provenance
 */

package lima

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Promoted Gate 0 image pin (docs/spike-results/v1-onboarding-20260727T115633Z/FINDINGS.md).
const (
	PromotedImageURL    = "https://cloud-images.ubuntu.com/releases/noble/release-20260705/ubuntu-24.04-server-cloudimg-arm64.img"
	PromotedImageDigest = "sha256:7df0201546f75b8bcc1044594c806c35749421ad3c9bc1be2a3ab806cfae39cc"
	// PromotedHermesCommit is the Hermes Agent pin from Gate 0. Init embeds it
	// for callers/docs; guest Hermes install is reconciled in bootstrap (Task 9).
	PromotedHermesCommit = "91546b8337068891cc0a6b834d89d0d9270fb3ec"
)

// Default VM resources for torio vm init (FINDINGS: product disk SHOULD be 60GiB).
const (
	DefaultCPUs   = 4
	DefaultMemory = "8GiB"
	DefaultDisk   = "60GiB"
)

// operatorUserPattern is the strict allowlist for Lima login / operator identity.
// Rejects shell metacharacters, spaces, slashes, quotes, and command substitution.
var operatorUserPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

//go:embed templates/torio.yaml
var embeddedTemplate []byte

// embeddedProjectShell is the guest entry point of an operator session, shipped
// as its own file so the helper Torio provisions and the helper the tests
// execute are the same bytes. Init injects it into the template's data-mode
// provision step at OperatorShellHelper.
//
//go:embed templates/torio-project-shell.sh
var embeddedProjectShell []byte

const (
	placeholderOperator = "__TORIO_OPERATOR_USER__"
	placeholderCPUs     = "__TORIO_CPUS__"
	placeholderMemory   = "__TORIO_MEMORY__"
	placeholderDisk     = "__TORIO_DISK__"
	// placeholderShellPath and placeholderShellContent carry the operator shell
	// helper into the template. The path comes from OperatorShellHelper so the
	// guest file and the remote argv can never drift apart.
	placeholderShellPath    = "__TORIO_PROJECT_SHELL_PATH__"
	placeholderShellContent = "__TORIO_PROJECT_SHELL__"
)

// InitOptions configures torio vm init. Zero values select documented defaults.
// OperatorUser must be the Lima login identity (host short name); empty is
// rejected — callers (CLI) resolve the current user before calling Init.
type InitOptions struct {
	CPUs         int
	Memory       string
	Disk         string
	OperatorUser string
}

// InitResult reports whether a VM was created and which image pin applies.
type InitResult struct {
	Created       bool
	ImageLocation string
	ImageDigest   string
}

func (o InitOptions) withDefaults() InitOptions {
	if o.CPUs == 0 {
		o.CPUs = DefaultCPUs
	}
	if strings.TrimSpace(o.Memory) == "" {
		o.Memory = DefaultMemory
	}
	if strings.TrimSpace(o.Disk) == "" {
		o.Disk = DefaultDisk
	}
	return o
}

// validateOperatorUser rejects empty or disallowed operator identities before they
// reach template substitution or typed guest argv (no shell metacharacters).
func validateOperatorUser(user string) error {
	user = strings.TrimSpace(user)
	if user == "" || user == placeholderOperator {
		return fmt.Errorf("operator user is required")
	}
	if !operatorUserPattern.MatchString(user) {
		return fmt.Errorf("operator user %q is not allowed", user)
	}
	return nil
}

func renderTemplate(opts InitOptions) ([]byte, error) {
	opts = opts.withDefaults()
	op := strings.TrimSpace(opts.OperatorUser)
	if err := validateOperatorUser(op); err != nil {
		return nil, err
	}
	if opts.CPUs < 1 {
		return nil, fmt.Errorf("cpus must be >= 1")
	}
	if strings.ContainsAny(opts.Memory, "\n\r") || strings.ContainsAny(opts.Disk, "\n\r") {
		return nil, fmt.Errorf("memory/disk must be a single-line size string")
	}

	text := string(embeddedTemplate)
	replacer := strings.NewReplacer(
		placeholderOperator, op,
		placeholderCPUs, strconv.Itoa(opts.CPUs),
		placeholderMemory, opts.Memory,
		placeholderDisk, opts.Disk,
		placeholderShellPath, OperatorShellHelper,
	)
	text = replacer.Replace(text)
	// The helper is injected after every other substitution, so the bytes that
	// reach the guest are exactly the bytes of the tested script and never
	// something the replacer rewrote.
	text, err := injectProjectShell(text)
	if err != nil {
		return nil, err
	}
	if strings.Contains(text, placeholderOperator) ||
		strings.Contains(text, placeholderCPUs) ||
		strings.Contains(text, placeholderMemory) ||
		strings.Contains(text, placeholderDisk) ||
		strings.Contains(text, placeholderShellPath) {
		return nil, fmt.Errorf("template placeholder left unsubstituted")
	}
	if !strings.Contains(text, "path: "+OperatorShellHelper) {
		return nil, fmt.Errorf("embedded template invariant broken: operator shell helper is not provisioned")
	}
	if !strings.Contains(text, "mounts: []") {
		return nil, fmt.Errorf("embedded template invariant broken: mounts must be empty")
	}
	if !strings.Contains(text, PromotedImageDigest) || !strings.Contains(text, PromotedImageURL) {
		return nil, fmt.Errorf("embedded template invariant broken: promoted image pin missing")
	}
	return []byte(text), nil
}

// injectProjectShell replaces the helper placeholder with the embedded script,
// re-indented under the YAML block scalar that carries it. The placeholder must
// be alone on its line: the indentation of that line is what makes the injected
// block well-formed, and a placeholder sharing a line with anything else would
// silently produce a template that no longer describes the helper.
func injectProjectShell(text string) (string, error) {
	lines := strings.Split(text, "\n")
	at := -1
	for i, line := range lines {
		if !strings.Contains(line, placeholderShellContent) {
			continue
		}
		if strings.TrimLeft(line, " ") != placeholderShellContent {
			return "", fmt.Errorf("%s must be alone on its line", placeholderShellContent)
		}
		if at >= 0 {
			return "", fmt.Errorf("%s appears more than once", placeholderShellContent)
		}
		at = i
	}
	if at < 0 {
		return "", fmt.Errorf("embedded template invariant broken: %s placeholder missing", placeholderShellContent)
	}

	script := strings.TrimRight(string(embeddedProjectShell), "\n")
	if strings.TrimSpace(script) == "" {
		return "", fmt.Errorf("embedded operator shell helper is empty")
	}
	indent := strings.TrimSuffix(lines[at], placeholderShellContent)
	block := strings.Split(script, "\n")
	for i, line := range block {
		if line == "" {
			continue // an empty line needs no indent, and must carry no trailing space
		}
		block[i] = indent + line
	}
	lines[at] = strings.Join(block, "\n")
	return strings.Join(lines, "\n"), nil
}

// writePrivateTemplate writes content to a private temp file, fsyncs, and
// returns the path. Caller MUST remove the file (typically via defer).
func writePrivateTemplate(content []byte) (path string, err error) {
	f, err := os.CreateTemp("", "torio-lima-*.yaml")
	if err != nil {
		return "", err
	}
	path = f.Name()
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(path)
	}
	if _, err := f.Write(content); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
