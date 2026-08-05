package lima

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Promoted Gate 0 image pin
// (archive/pre-oss:docs/spike-results/v1-onboarding-20260727T115633Z/FINDINGS.md).
const (
	PromotedImageURL    = "https://cloud-images.ubuntu.com/releases/noble/release-20260705/ubuntu-24.04-server-cloudimg-arm64.img"
	PromotedImageDigest = "sha256:7df0201546f75b8bcc1044594c806c35749421ad3c9bc1be2a3ab806cfae39cc"
	// PromotedHermesCommit is the Hermes Agent pin from Gate 0. Init embeds it
	// for callers/docs; guest Hermes install is reconciled in bootstrap.
	// Re-promoted 2026-08-03: wzslr321/hermes-agent 0a62610 (descendant of the
	// Gate 0 pin 91546b8; picks up the openclaw EXDEV fsync fix).
	PromotedHermesCommit = "0a62610f10cc34d696b2239b2c69fa1ba0f1ca63"
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

// embeddedProjectEnter is the guest entry point of an ordinary project
// session. It is provisioned separately from the push-capable shell helper so
// the guest prompt and the transport cannot confuse the two capabilities.
//
//go:embed templates/torio-project-enter.sh
var embeddedProjectEnter []byte

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
	placeholderEnterPath    = "__TORIO_PROJECT_ENTER_PATH__"
	placeholderEnterContent = "__TORIO_PROJECT_ENTER__"
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
// ValidateOperatorUser accepts only a guest login name safe to place in a fixed
// argv. It is exported because the guest session's own identity is read back at
// runtime — `limactl copy` writes as that user, so staging must be owned by it —
// and a name read from the guest is an input, not a constant.
func ValidateOperatorUser(user string) error { return validateOperatorUser(user) }

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
		placeholderEnterPath, ProjectEnterHelper,
	)
	text = replacer.Replace(text)
	// The helper is injected after every other substitution, so the bytes that
	// reach the guest are exactly the bytes of the tested script and never
	// something the replacer rewrote.
	text, err := injectProjectShell(text)
	if err != nil {
		return nil, err
	}
	text, err = injectProjectEnter(text)
	if err != nil {
		return nil, err
	}
	if strings.Contains(text, placeholderOperator) ||
		strings.Contains(text, placeholderCPUs) ||
		strings.Contains(text, placeholderMemory) ||
		strings.Contains(text, placeholderDisk) ||
		strings.Contains(text, placeholderShellPath) ||
		strings.Contains(text, placeholderEnterPath) {
		return nil, fmt.Errorf("template placeholder left unsubstituted")
	}
	if !strings.Contains(text, "path: "+OperatorShellHelper) {
		return nil, fmt.Errorf("embedded template invariant broken: operator shell helper is not provisioned")
	}
	if !strings.Contains(text, "path: "+ProjectEnterHelper) {
		return nil, fmt.Errorf("embedded template invariant broken: project enter helper is not provisioned")
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
	return injectProjectHelper(text, placeholderShellContent, embeddedProjectShell, "operator shell")
}

func injectProjectEnter(text string) (string, error) {
	return injectProjectHelper(text, placeholderEnterContent, embeddedProjectEnter, "project enter")
}

func injectProjectHelper(text, placeholder string, content []byte, label string) (string, error) {
	lines := strings.Split(text, "\n")
	at := -1
	for i, line := range lines {
		if !strings.Contains(line, placeholder) {
			continue
		}
		if strings.TrimLeft(line, " ") != placeholder {
			return "", fmt.Errorf("%s must be alone on its line", placeholder)
		}
		if at >= 0 {
			return "", fmt.Errorf("%s appears more than once", placeholder)
		}
		at = i
	}
	if at < 0 {
		return "", fmt.Errorf("embedded template invariant broken: %s placeholder missing", placeholder)
	}

	script := strings.TrimRight(string(content), "\n")
	if strings.TrimSpace(script) == "" {
		return "", fmt.Errorf("embedded %s helper is empty", label)
	}
	indent := strings.TrimSuffix(lines[at], placeholder)
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
