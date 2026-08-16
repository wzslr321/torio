package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// maxProjects bounds the registry. Config is read on every invocation and is
	// operator-authored, so its size stays explicitly bounded rather than open.
	maxProjects = 64
	// maxProjectIDLen bounds the slug that also derives the workspace path.
	maxProjectIDLen = 64
	// maxDisplayNameLen bounds the human label.
	maxDisplayNameLen = 64
	// maxRemoteLen bounds the Git remote URL.
	maxRemoteLen = 512
)

var (
	// projectIDPattern is the accepted project ID: a lowercase slug of ASCII
	// letters, digits and inner hyphens, bounded to maxProjectIDLen. Nothing in
	// this charset can traverse, rename or escape a directory, which is what
	// makes it safe to derive the workspace path from.
	projectIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	// remoteHostPattern is the accepted host of a remote (a DNS name or an SSH
	// config alias). It excludes anything that could be read as another URL
	// component or as an argument.
	remoteHostPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,252}[A-Za-z0-9])?$`)
	// remoteUserPattern is the accepted SSH username. A username is non-secret
	// transport (`git@…`), so it is allowed — a password never is.
	remoteUserPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	// remoteURLPathPattern is the accepted repository path of an https:// or
	// ssh:// remote: absolute and non-empty.
	remoteURLPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._~+/-]+$`)
	// scpRemotePattern is the accepted scp-like form `[user@]host:path`. The
	// path must be relative: an absolute one would make `C:/repo` — a local
	// Windows path — look like a remote.
	scpRemotePattern = regexp.MustCompile(`^(?:([A-Za-z0-9._-]{1,64})@)?([A-Za-z0-9][A-Za-z0-9.-]{0,252}):([A-Za-z0-9._~+-][A-Za-z0-9._~+/-]*)$`)
	// remotePortPattern is the accepted explicit port.
	remotePortPattern = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// Registry errors. They are sentinels so the projects/CLI layers can map a
// conflict to a stable exit kind instead of matching on message text.
var (
	// ErrDuplicateProjectID reports an ID that is already registered.
	ErrDuplicateProjectID = errors.New("project id is already registered")
	// ErrDuplicateRemote reports a remote that is already registered by another
	// project. It is rejected by default and only allowed by an explicit
	// operator decision (see AddOptions).
	ErrDuplicateRemote = errors.New("remote is already registered by another project")
	// ErrProjectNotFound reports an ID that is not in the registry.
	ErrProjectNotFound = errors.New("project id is not registered")
)

// Project is one attached repository in the active registry. Every field is
// non-secret operator intent: a stable identity, a human label, and the Git
// remote used to reach the code.
//
// The workspace path is deliberately not a field. It is derived from ID by the
// projects layer (`<backend workspace>/<id>`, ADR-0003), so config can never
// point a project at an arbitrary location on the guest.
type Project struct {
	// ID is the stable slug identifying the project. It also derives the
	// workspace path, so its charset is restricted (see projectIDPattern).
	ID string
	// DisplayName is the human-readable label shown to the operator and used to
	// register the project with the backend.
	DisplayName string
	// Remote is the Git remote URL. It must be a supported HTTPS/SSH/scp form
	// and must never carry an embedded credential (see validateRemote).
	Remote string
}

// AddOptions carries the explicit decisions a registry addition may need. The
// zero value is the strict default.
type AddOptions struct {
	// AllowDuplicateRemote permits registering a remote another project already
	// uses. Two projects sharing one remote is almost always a mistake (the
	// second workspace silently shadows the first), so it takes a deliberate
	// operator decision rather than being accepted silently.
	AllowDuplicateRemote bool
}

// WithProject returns a copy of f with p appended to the registry. It is a
// value operation: f and its slice are never mutated, so a failed addition
// cannot leave a half-updated document behind.
//
// The addition is validated before anything else happens, and the resulting
// document is validated as a whole, so an invalid entry can never reach the
// write path. The returned document declares the current schema version: the
// first mutation of a V1 document upgrades it to V2.
func (f File) WithProject(p Project, opts AddOptions) (_ File, err error) {
	defer func() { err = redactErr(err) }()

	if err := p.validate(); err != nil {
		return File{}, err
	}
	for _, existing := range f.Projects {
		if existing.ID == p.ID {
			return File{}, fmt.Errorf("%w: %q", ErrDuplicateProjectID, p.ID)
		}
		// Two local projects each have no remote, which is not two projects
		// sharing one: the rule exists because a second workspace on one remote
		// silently shadows the first, and an absent remote shadows nothing.
		if !opts.AllowDuplicateRemote && p.Remote != "" && existing.Remote == p.Remote {
			// The conflicting remote is not echoed; the owning project is enough
			// to act on and cannot carry secret-shaped material.
			return File{}, fmt.Errorf("%w: project %q", ErrDuplicateRemote, existing.ID)
		}
	}

	out := f
	out.SchemaVersion = ConfigSchemaVersion
	out.Projects = append(append(make([]Project, 0, len(f.Projects)+1), f.Projects...), p)
	if err := out.Validate(); err != nil {
		return File{}, err
	}
	return out, nil
}

// WithUpdatedRemote returns a copy of f with the remote of project id replaced.
//
// It is the correction path for a record whose remote turned out to name
// something the guests cannot reach (ADR-0023). Only the remote moves: the id
// is what every derived path and every checkout is named from, and the display
// name is what the backend's own registry knows the project by, so a
// correction that quietly changed either would be a different project under an
// unchanged name.
//
// It validates exactly what an addition validates, including the duplicate
// remote rule, so a correction can never write a document an addition could
// not have written.
func (f File) WithUpdatedRemote(id, remote string, opts AddOptions) (_ File, err error) {
	defer func() { err = redactErr(err) }()

	updated := make([]Project, 0, len(f.Projects))
	found := false
	for _, p := range f.Projects {
		if p.ID != id {
			// As in WithProject: an absent remote is not a remote two projects
			// can share, so it never collides.
			if !opts.AllowDuplicateRemote && remote != "" && p.Remote == remote {
				return File{}, fmt.Errorf("%w: project %q", ErrDuplicateRemote, p.ID)
			}
			updated = append(updated, p)
			continue
		}
		found = true
		p.Remote = remote
		if err := p.validate(); err != nil {
			return File{}, err
		}
		updated = append(updated, p)
	}
	if !found {
		return File{}, fmt.Errorf("%w: %q", ErrProjectNotFound, id)
	}

	out := f
	out.SchemaVersion = ConfigSchemaVersion
	out.Projects = updated
	if err := out.Validate(); err != nil {
		return File{}, err
	}
	return out, nil
}

// WithoutProject returns a copy of f with project id removed. It is a value
// operation like WithProject, and it reports ErrProjectNotFound when the ID is
// absent so a caller can tell "already gone" from "removed now" — a rerun after
// an interrupted removal must be able to finish, not to fail blindly.
func (f File) WithoutProject(id string) (_ File, err error) {
	defer func() { err = redactErr(err) }()

	kept := make([]Project, 0, len(f.Projects))
	found := false
	for _, p := range f.Projects {
		if p.ID == id {
			found = true
			continue
		}
		kept = append(kept, p)
	}
	if !found {
		return File{}, fmt.Errorf("%w: %q", ErrProjectNotFound, id)
	}

	out := f
	out.SchemaVersion = ConfigSchemaVersion
	out.Projects = kept
	if err := out.Validate(); err != nil {
		return File{}, err
	}
	return out, nil
}

// validateProjects enforces the registry-wide rules: a bounded number of
// entries, every entry valid, and unique IDs.
//
// Duplicate remotes are deliberately not rejected here. They are refused at the
// addition boundary (WithProject) unless the operator explicitly allows them;
// once such a decision has been made and persisted, the document must stay
// readable — otherwise the explicit decision would brick the config.
func validateProjects(projects []Project) error {
	if len(projects) > maxProjects {
		return fmt.Errorf("registry holds %d projects, more than the maximum %d", len(projects), maxProjects)
	}
	seen := make(map[string]struct{}, len(projects))
	for _, p := range projects {
		if err := p.validate(); err != nil {
			return err
		}
		if _, dup := seen[p.ID]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateProjectID, p.ID)
		}
		seen[p.ID] = struct{}{}
	}
	return nil
}

// validate enforces the per-entry rules. A rejected display name or remote is
// never echoed back: the remote is the field an embedded credential would live
// in, and naming the broken rule is what the operator needs anyway. The ID is
// echoed because it is a bounded slug the operator typed and must correct — and
// %q escapes any non-printable byte in it.
func (p Project) validate() error {
	if err := validateProjectID(p.ID); err != nil {
		return err
	}
	if err := validateDisplayName(p.DisplayName); err != nil {
		return fmt.Errorf("project %q display_name: %w", p.ID, err)
	}
	if err := validateRemote(p.Remote); err != nil {
		return fmt.Errorf("project %q remote: %w", p.ID, err)
	}
	return nil
}

func validateProjectID(id string) error {
	if id == "" {
		return errors.New("project id is required")
	}
	if len(id) > maxProjectIDLen {
		return fmt.Errorf("project id is longer than %d bytes", maxProjectIDLen)
	}
	if containsSecretShape(id) {
		return errors.New("project id contains secret-shaped material; config must be non-secret")
	}
	if !projectIDPattern.MatchString(id) {
		return fmt.Errorf("project id %q must be a lowercase slug of letters, digits and inner hyphens", id)
	}
	return nil
}

func validateDisplayName(name string) error {
	if name == "" {
		return errors.New("is required")
	}
	if len(name) > maxDisplayNameLen {
		return fmt.Errorf("is longer than %d bytes", maxDisplayNameLen)
	}
	if !utf8.ValidString(name) {
		return errors.New("is not valid UTF-8")
	}
	if containsSecretShape(name) {
		return errors.New("contains secret-shaped material; config must be non-secret")
	}
	if hasControlRune(name) {
		return errors.New("contains control characters")
	}
	if strings.TrimSpace(name) != name {
		return errors.New("has leading or trailing whitespace")
	}
	return nil
}

// validateRemote accepts only the transport forms Torio can reach a repository
// with, and refuses everything that could carry a credential, address the local
// filesystem, or be re-read as an argument by Git:
//
//   - the empty string — the project is local, living only in the guest it was
//     made in, with no remote to record (ADR-0027);
//   - https://host[:port]/path — no userinfo at all, since HTTPS userinfo is
//     exactly where a token or password would be embedded;
//   - ssh://[user@]host[:port]/path — a username is non-secret transport and is
//     allowed, a password is not;
//   - [user@]host:path — the scp-like form, with a relative path.
//
// Query and fragment are rejected on every form (they can carry tokens),
// percent-encoding is rejected (it hides the above), and a local path or
// file:// URL is not a remote at all. The absence of a remote and a remote
// naming the local filesystem are different things: the first says there is
// nothing to reach, the second names something only one machine can reach, and
// only the first is a project.
func validateRemote(remote string) error {
	if remote == "" {
		return nil
	}
	switch {
	case len(remote) > maxRemoteLen:
		return fmt.Errorf("is longer than %d bytes", maxRemoteLen)
	case !utf8.ValidString(remote):
		return errors.New("is not valid UTF-8")
	case containsSecretShape(remote):
		return errors.New("contains secret-shaped material; config must be non-secret")
	case hasControlRune(remote) || strings.ContainsAny(remote, " \t"):
		return errors.New("contains control characters or whitespace")
	case strings.Contains(remote, "%"):
		return errors.New("must not use percent-encoding")
	case strings.HasPrefix(remote, "-"):
		// A leading dash would be read as a flag by any Git invocation.
		return errors.New("must not start with '-'")
	}

	switch {
	case strings.HasPrefix(remote, "https://"), strings.HasPrefix(remote, "ssh://"):
		return validateRemoteURL(remote)
	case scpRemotePattern.MatchString(remote):
		m := scpRemotePattern.FindStringSubmatch(remote)
		user, host := m[1], m[2]
		if user != "" && !remoteUserPattern.MatchString(user) {
			return errors.New("has an unsupported ssh username")
		}
		if !remoteHostPattern.MatchString(host) {
			return errors.New("has an unsupported host")
		}
		return nil
	default:
		return errors.New("must be an https://, ssh:// or [user@]host:path Git remote")
	}
}

// validateRemoteURL enforces the rules of the https:// and ssh:// forms.
func validateRemoteURL(remote string) error {
	u, err := url.Parse(remote)
	if err != nil {
		return errors.New("is not a parsable URL")
	}
	if u.Opaque != "" {
		return errors.New("is not a hierarchical URL")
	}
	if u.ForceQuery || u.RawQuery != "" {
		return errors.New("must not carry a query")
	}
	if u.Fragment != "" {
		return errors.New("must not carry a fragment")
	}
	switch u.Scheme {
	case "https":
		// Any HTTPS userinfo is a credential in practice (`token@`,
		// `user:password@`), so the whole component is refused.
		if u.User != nil {
			return errors.New("must not embed credentials in the URL")
		}
	case "ssh":
		if u.User != nil {
			if _, hasPassword := u.User.Password(); hasPassword {
				return errors.New("must not embed a password in the URL")
			}
			if !remoteUserPattern.MatchString(u.User.Username()) {
				return errors.New("has an unsupported ssh username")
			}
		}
	default:
		return errors.New("must be an https://, ssh:// or [user@]host:path Git remote")
	}
	if !remoteHostPattern.MatchString(u.Hostname()) {
		return errors.New("has an unsupported host")
	}
	if port := u.Port(); port != "" && !remotePortPattern.MatchString(port) {
		return errors.New("has an unsupported port")
	}
	if !remoteURLPathPattern.MatchString(u.Path) {
		return errors.New("must name a repository path")
	}
	return nil
}

// hasControlRune reports whether s carries a control or non-printable rune —
// including a terminal escape, which must never reach an operator's terminal or
// a Git argv.
func hasControlRune(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return true
		}
	}
	return false
}
