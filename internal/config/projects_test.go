package config

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// v2 returns a V2 document carrying projects, so a test asserts on registry
// rules only — never on an incidental schema/timeout failure.
func v2(projects ...Project) File {
	return File{SchemaVersion: ConfigSchemaVersion, Projects: projects}
}

// sampleProject is a minimal valid registry entry.
func sampleProject() Project {
	return Project{ID: "my-project", DisplayName: "My Project", Remote: "git@github.com:owner/my-project.git"}
}

func TestFileValidateAcceptsWellFormedRegistry(t *testing.T) {
	f := v2(
		Project{ID: "a", DisplayName: "A", Remote: "https://github.com/owner/a.git"},
		Project{ID: "my-project-2", DisplayName: "My Project 2", Remote: "git@github.com:owner/my-project-2.git"},
	)
	if err := f.Validate(); err != nil {
		t.Fatalf("well-formed registry must validate, got %v", err)
	}
}

// TestFileValidateRejectsInvalidProjectID bounds the ID hard because it is not
// just an identifier: the workspace path is derived from it, so anything that
// could escape or rename a directory must never reach the filesystem layer.
func TestFileValidateRejectsInvalidProjectID(t *testing.T) {
	for _, id := range []string{
		"",                             // empty
		"My-Project",                   // uppercase
		"my_project",                   // underscore is not part of the slug charset
		"-my-project",                  // leading hyphen
		"my-project-",                  // trailing hyphen
		"my project",                   // space
		"my.project",                   // dot
		".",                            // current directory
		"..",                           // parent directory
		"../escape",                    // traversal
		"my/project",                   // separator
		"my\\project",                  // separator (Windows form)
		"projekt-żółw",                 // non-ASCII
		"my\nproject",                  // newline
		"my\x00project",                // NUL
		strings.Repeat("a", 65),        // over the length bound
		"sk-abcdefghijklmnopqrstuvwxy", // slug charset, but a secret shape
	} {
		p := sampleProject()
		p.ID = id
		if err := v2(p).Validate(); err == nil {
			t.Errorf("project id %q must be rejected", id)
		}
	}
}

func TestFileValidateRejectsDuplicateProjectID(t *testing.T) {
	a := sampleProject()
	b := sampleProject()
	b.Remote = "https://github.com/owner/other.git"
	if err := v2(a, b).Validate(); err == nil {
		t.Fatalf("duplicate project id must be rejected")
	}
}

func TestFileValidateRejectsInvalidDisplayName(t *testing.T) {
	for _, name := range []string{
		"",                      // empty
		"   ",                   // whitespace only
		" My Project",           // leading whitespace
		"My Project ",           // trailing whitespace
		"My\nProject",           // newline
		"My\tProject",           // control character
		"My\x1b[31mProject",     // terminal escape
		strings.Repeat("n", 65), // over the length bound
		secretCanary,            // secret-shaped
	} {
		p := sampleProject()
		p.DisplayName = name
		if err := v2(p).Validate(); err == nil {
			t.Errorf("display name %q must be rejected", name)
		}
	}
}

// TestFileValidateAcceptsSupportedRemoteForms pins the transport forms V1
// supports. An SSH username is transport, not a credential, so it stays.
func TestFileValidateAcceptsSupportedRemoteForms(t *testing.T) {
	for _, remote := range []string{
		"https://github.com/owner/repo.git",
		"https://gitlab.example.com:8443/group/sub/repo.git",
		"ssh://git@github.com/owner/repo.git",
		"ssh://git@github.com:2222/owner/repo.git",
		"ssh://github-work/owner/repo.git",
		"git@github.com:owner/repo.git",
		"github-work:owner/repo.git",
	} {
		p := sampleProject()
		p.Remote = remote
		if err := v2(p).Validate(); err != nil {
			t.Errorf("remote %q must be accepted: %v", remote, err)
		}
	}
}

// TestFileValidateRejectsUnsupportedRemote is the credential boundary: config is
// non-secret, so every form that can carry a password/token, address the local
// filesystem, or smuggle control characters into a Git argv is refused.
func TestFileValidateRejectsUnsupportedRemote(t *testing.T) {
	for _, remote := range []string{
		"", // empty
		"https://user:pass@github.com/owner/repo.git",             // password in userinfo
		"https://token@github.com/owner/repo.git",                 // token in userinfo
		"ssh://git:pass@github.com/owner/repo.git",                // password in userinfo
		"https://github.com/owner/repo.git?token=abc",             // query
		"https://github.com/owner/repo.git#frag",                  // fragment
		"https://github.com/owner/repo.git?",                      // empty query is still a query
		"http://github.com/owner/repo.git",                        // not HTTPS
		"git://github.com/owner/repo.git",                         // unsupported scheme
		"file:///srv/git/repo.git",                                // local file URL
		"/srv/git/repo.git",                                       // absolute local path
		"./repo",                                                  // relative local path
		"../repo",                                                 // relative local path
		"~/repo",                                                  // home-relative local path
		"repo.git",                                                // bare local name
		"C:/repo",                                                 // Windows local path
		"git@github.com:/owner/repo.git",                          // absolute scp path
		"--upload-pack=/bin/sh",                                   // argv flag injection
		"-oProxyCommand=id",                                       // argv flag injection
		"https://github.com/owner/repo.git\n",                     // newline
		"https://github.com/owner/re\x00po.git",                   // NUL
		"https://github.com/owner/ repo.git",                      // whitespace
		"https://github.com/owner/%72epo.git",                     // percent-encoding
		"https://github.com/",                                     // no repository path
		"https://" + secretCanary + "@github.com/o/r.git",         // secret-shaped userinfo
		"https://github.com/" + strings.Repeat("a", 512) + ".git", // over the length bound
	} {
		p := sampleProject()
		p.Remote = remote
		if err := v2(p).Validate(); err == nil {
			t.Errorf("remote %q must be rejected", remote)
		}
	}
}

// TestFileValidateRejectsTooManyProjects keeps the registry bounded: config is
// read on every invocation, so its size cannot be open-ended.
func TestFileValidateRejectsTooManyProjects(t *testing.T) {
	f := File{SchemaVersion: ConfigSchemaVersion}
	for i := 0; i <= maxProjects; i++ {
		n := strconv.Itoa(i)
		f.Projects = append(f.Projects, Project{
			ID:          "p" + n,
			DisplayName: "P" + n,
			Remote:      "https://github.com/owner/repo" + n + ".git",
		})
	}
	if err := f.Validate(); err == nil {
		t.Fatalf("more than %d projects must be rejected", maxProjects)
	}
}

// TestWithProjectStampsSchemaVersionAndKeepsSettings locks the write half of a
// first mutation: a document that carries settings but no version — the in-memory
// shape of a first run, where no file was read — is stamped with the current
// version and keeps its settings, so WriteFile never has to reject what the
// mutation helper just produced.
func TestWithProjectStampsSchemaVersionAndKeepsSettings(t *testing.T) {
	unstamped := File{Timeout: 45 * time.Second}
	got, err := unstamped.WithProject(sampleProject(), AddOptions{})
	if err != nil {
		t.Fatalf("WithProject: %v", err)
	}
	if got.SchemaVersion != ConfigSchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", got.SchemaVersion, ConfigSchemaVersion)
	}
	if got.Timeout != 45*time.Second {
		t.Errorf("Timeout = %v, want the settings to survive the mutation", got.Timeout)
	}
	if len(got.Projects) != 1 || got.Projects[0] != sampleProject() {
		t.Errorf("Projects = %+v, want the added project", got.Projects)
	}
	// The receiver is a value: the original document must be untouched, so a
	// caller can never persist a half-updated registry.
	if len(unstamped.Projects) != 0 || unstamped.SchemaVersion != "" {
		t.Errorf("receiver was mutated: %+v", unstamped)
	}
}

func TestWithProjectRejectsInvalidProject(t *testing.T) {
	p := sampleProject()
	p.ID = "Not A Slug"
	if _, err := v2().WithProject(p, AddOptions{}); err == nil {
		t.Fatalf("invalid project must be rejected before it enters the registry")
	}
}

func TestWithProjectRejectsDuplicateID(t *testing.T) {
	base, err := v2().WithProject(sampleProject(), AddOptions{})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	dup := sampleProject()
	dup.Remote = "https://github.com/owner/other.git"
	_, err = base.WithProject(dup, AddOptions{})
	if !errors.Is(err, ErrDuplicateProjectID) {
		t.Fatalf("duplicate id error = %v, want ErrDuplicateProjectID", err)
	}
}

// TestWithProjectRejectsDuplicateRemoteUnlessAllowed pins the default: two
// projects on one remote is a mistake often enough that it takes an explicit
// decision, not a silent accept.
func TestWithProjectRejectsDuplicateRemoteUnlessAllowed(t *testing.T) {
	base, err := v2().WithProject(sampleProject(), AddOptions{})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	second := Project{ID: "other-project", DisplayName: "Other", Remote: sampleProject().Remote}

	if _, err := base.WithProject(second, AddOptions{}); !errors.Is(err, ErrDuplicateRemote) {
		t.Fatalf("duplicate remote error = %v, want ErrDuplicateRemote", err)
	}

	got, err := base.WithProject(second, AddOptions{AllowDuplicateRemote: true})
	if err != nil {
		t.Fatalf("explicitly allowed duplicate remote must be accepted: %v", err)
	}
	if len(got.Projects) != 2 {
		t.Errorf("Projects = %+v, want both entries", got.Projects)
	}
	// A persisted explicit decision must stay readable, or it would brick the
	// config the operator was allowed to write.
	if err := got.Validate(); err != nil {
		t.Errorf("document with an allowed duplicate remote must validate: %v", err)
	}
}

func TestWithoutProjectRemovesEntryAndReportsUnknownID(t *testing.T) {
	base, err := v2().WithProject(sampleProject(), AddOptions{})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := base.WithoutProject(sampleProject().ID)
	if err != nil {
		t.Fatalf("WithoutProject: %v", err)
	}
	if len(got.Projects) != 0 {
		t.Errorf("Projects = %+v, want empty", got.Projects)
	}
	if len(base.Projects) != 1 {
		t.Errorf("receiver was mutated: %+v", base)
	}

	// Distinguishable "already gone" so an interrupted removal can be rerun.
	if _, err := got.WithoutProject(sampleProject().ID); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("unknown id error = %v, want ErrProjectNotFound", err)
	}
}

// TestFileValidateDoesNotLeakSecretShapedField proves the registry honours the
// package's no-leak contract: a rejected field that carries a secret shape is
// never echoed back, whichever field it landed in.
func TestFileValidateDoesNotLeakSecretShapedField(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project Project
	}{
		{"id", Project{ID: "sk-" + strings.Repeat("a", 24), DisplayName: "N", Remote: "https://github.com/o/r.git"}},
		{"display_name", Project{ID: "p", DisplayName: secretCanary, Remote: "https://github.com/o/r.git"}},
		{"remote", Project{ID: "p", DisplayName: "N", Remote: "https://" + secretCanary + "@github.com/o/r.git"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := v2(tc.project).Validate()
			if err == nil {
				t.Fatalf("secret-shaped %s must be rejected", tc.name)
			}
			if strings.Contains(err.Error(), secretCanary) || strings.Contains(err.Error(), "sk-"+strings.Repeat("a", 24)) {
				t.Errorf("error leaked secret-shaped %s: %q", tc.name, err.Error())
			}
		})
	}
}
