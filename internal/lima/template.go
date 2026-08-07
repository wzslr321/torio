package lima

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
)

// The promoted Gate 0 image pin is now per-host and lives in Profile: the two
// supported hosts run the same Ubuntu build compiled for different machines,
// so a single pair of constants could only have described one of them.
//
// PromotedHermesCommit is the Hermes Agent pin from Gate 0. Init embeds it for
// callers/docs; guest Hermes install is reconciled in bootstrap. It is not
// host-derived — Hermes is built in the guest — so it stays a constant.
// Re-promoted 2026-08-03: wzslr321/hermes-agent 0a62610 (descendant of the
// Gate 0 pin 91546b8; picks up the openclaw EXDEV fsync fix).
const PromotedHermesCommit = "0a62610f10cc34d696b2239b2c69fa1ba0f1ca63"

// Default VM resources for torio vm init (product disk SHOULD be 60GiB).
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
	// The host-derived pins. They are substituted from the same Profile that
	// verifyCompatibleConfig checks the created instance against.
	// placeholderBackendProvision carries the declared backend's own guest
	// identity and directory layout. It is a placeholder rather than a fixed
	// block because which identity the guest is built for is the one thing a
	// second backend must change about provisioning.
	placeholderBackendProvision = "__TORIO_BACKEND_PROVISION__"

	placeholderVMType      = "__TORIO_VMTYPE__"
	placeholderArch        = "__TORIO_ARCH__"
	placeholderImageURL    = "__TORIO_IMAGE_URL__"
	placeholderImageDigest = "__TORIO_IMAGE_DIGEST__"
)

// InitOptions configures torio vm init. Zero values select documented defaults.
// OperatorUser must be the Lima login identity (host short name); empty is
// rejected — callers (CLI) resolve the current user before calling Init.
type InitOptions struct {
	CPUs         int
	Memory       string
	Disk         string
	OperatorUser string
	// Backend is the agent backend this instance is created for. Its
	// provisioning block becomes the guest's identity and layout, so the VM
	// comes up built for exactly one agent. A nil Backend is the default one,
	// which is what every instance created before instances declared a backend
	// is running.
	Backend backend.Backend
}

// InitResult reports whether a VM was created and which image pin applies.
type InitResult struct {
	Created       bool
	ImageLocation string
	ImageDigest   string
}

func (o InitOptions) withDefaults() InitOptions {
	if o.Backend == nil {
		o.Backend = Hermes()
	}
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

func renderTemplate(opts InitOptions, profile Profile) ([]byte, error) {
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
	// A partial profile would render a template with empty pins that the
	// verifier would then compare against the same empty pins and accept.
	if !profile.valid() {
		return nil, fmt.Errorf("incomplete host profile; refusing to render a template with unpinned vmType, arch or image")
	}

	text := string(embeddedTemplate)
	replacer := strings.NewReplacer(
		placeholderOperator, op,
		placeholderCPUs, strconv.Itoa(opts.CPUs),
		placeholderMemory, opts.Memory,
		placeholderDisk, opts.Disk,
		placeholderShellPath, OperatorShellHelper,
		placeholderEnterPath, ProjectEnterHelper,
		placeholderVMType, profile.VMType,
		placeholderArch, profile.Arch,
		placeholderImageURL, profile.ImageURL,
		placeholderImageDigest, profile.ImageDigest,
		placeholderBackendProvision, indentBlock(opts.Backend.ProvisionScript(), "      "),
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
		strings.Contains(text, placeholderEnterPath) ||
		strings.Contains(text, placeholderVMType) ||
		strings.Contains(text, placeholderArch) ||
		strings.Contains(text, placeholderImageURL) ||
		strings.Contains(text, placeholderImageDigest) ||
		strings.Contains(text, placeholderBackendProvision) {
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
	if !strings.Contains(text, profile.ImageDigest) || !strings.Contains(text, profile.ImageURL) {
		return nil, fmt.Errorf("embedded template invariant broken: promoted image pin missing")
	}
	// The rendered document must carry the driver and architecture the verifier
	// will demand. Checking for the substituted values rather than the absence
	// of placeholders catches the case the placeholder check cannot: a template
	// edited to spell a literal driver, which would render successfully and
	// then fail verification against a profile that says something else.
	if !strings.Contains(text, "vmType: "+profile.VMType) {
		return nil, fmt.Errorf("embedded template invariant broken: vmType is not the profile's %q", profile.VMType)
	}
	if !strings.Contains(text, "arch: "+profile.Arch) {
		return nil, fmt.Errorf("embedded template invariant broken: arch is not the profile's %q", profile.Arch)
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

// indentBlock re-indents a multi-line provisioning block to sit inside the
// template's YAML script literal. A backend writes plain shell; where that
// shell lands in the document is the template's problem, not the backend's.
func indentBlock(text, indent string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ""
			continue
		}
		// The first line already sits at the placeholder's own indentation.
		if i == 0 {
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}
