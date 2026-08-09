package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// requireTrustPolicy skips a test on hosts where the trusted-authority policy
// is not claimed. It mirrors the darwin || linux build constraint of the
// enforcement primitive (trust_darwinlinux.go); on every other host the
// primitive is a documented no-op (trust_other.go), so a rejection assertion
// would spuriously fail. Ordinary functional tests stay unconditional; only
// enforcement assertions gate on this.
func requireTrustPolicy(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("trusted-authority enforcement is claimed only on darwin/linux, not %s", runtime.GOOS)
	}
}

// loadWith resolves+loads config with a temp XDG_CONFIG_HOME so no real
// user config is touched. It returns the Runtime and any error.
func loadWith(t *testing.T, opts Options, cfgHome string) (Runtime, error) {
	t.Helper()
	env := map[string]string{}
	if cfgHome != "" {
		env["XDG_CONFIG_HOME"] = cfgHome
	}
	opts.Getenv = envFunc(env)
	if opts.HomeDir == nil {
		opts.HomeDir = homeFunc(t.TempDir())
	}
	return Load(opts)
}

func writeConfig(t *testing.T, cfgHome, body string) string {
	t.Helper()
	dir := filepath.Join(cfgHome, appDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadAbsentDefaultConfigIsValidFirstRun(t *testing.T) {
	rt, err := loadWith(t, Options{}, t.TempDir())
	if err != nil {
		t.Fatalf("absent default config must be valid, got %v", err)
	}
	if rt.ConfigLoaded {
		t.Errorf("ConfigLoaded = true, want false for absent default config")
	}
	if rt.File.Timeout != 0 {
		t.Errorf("absent config Timeout = %v, want 0 (unset)", rt.File.Timeout)
	}
}

func TestLoadExplicitMissingConfigIsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	_, err := loadWith(t, Options{ConfigPath: missing}, t.TempDir())
	if err == nil {
		t.Fatalf("explicit --config to a missing file must error")
	}
}

func TestLoadValidConfigParsesFields(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"2","default_timeout":"45s"}`)
	rt, err := loadWith(t, Options{}, cfgHome)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !rt.ConfigLoaded {
		t.Errorf("ConfigLoaded = false, want true")
	}
	if rt.File.Timeout != 45*time.Second {
		t.Errorf("Timeout = %v, want 45s", rt.File.Timeout)
	}
}

// TestLoadV2ConfigParsesProjectRegistry locks the V2 document: the registry is
// read into typed projects and carries only non-secret identity/remote — no
// workspace path, which is derived from the ID by the projects layer.
func TestLoadV2ConfigParsesProjectRegistry(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"2","default_timeout":"45s","projects":[`+
		`{"id":"my-project","display_name":"My Project","remote":"git@github.com:owner/my-project.git"}]}`)
	rt, err := loadWith(t, Options{}, cfgHome)
	if err != nil {
		t.Fatalf("Load V2: %v", err)
	}
	if rt.File.SchemaVersion != ConfigSchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", rt.File.SchemaVersion, ConfigSchemaVersion)
	}
	if rt.File.Timeout != 45*time.Second {
		t.Errorf("Timeout = %v, want 45s", rt.File.Timeout)
	}
	want := []Project{{ID: "my-project", DisplayName: "My Project", Remote: "git@github.com:owner/my-project.git"}}
	if len(rt.File.Projects) != len(want) {
		t.Fatalf("Projects = %+v, want %+v", rt.File.Projects, want)
	}
	if rt.File.Projects[0] != want[0] {
		t.Errorf("Projects[0] = %+v, want %+v", rt.File.Projects[0], want[0])
	}
}

// TestLoadOmittedProjectsNormalizesToEmptyRegistry locks the settings-only
// shape of the current schema: "projects" is optional, and a document without
// it loads with an empty registry rather than failing.
func TestLoadOmittedProjectsNormalizesToEmptyRegistry(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"2","default_timeout":"45s"}`)
	rt, err := loadWith(t, Options{}, cfgHome)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rt.File.SchemaVersion != ConfigSchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", rt.File.SchemaVersion, ConfigSchemaVersion)
	}
	if len(rt.File.Projects) != 0 {
		t.Errorf("Projects = %+v, want empty when the document omits them", rt.File.Projects)
	}
}

// TestLoadRejectsSettingsOnlySchemaVersion pins the removal of the pre-registry
// schema: a document declaring "1" is rejected by version, not read as
// settings-only. Torio never shipped a release that wrote one (ADR-0001).
func TestLoadRejectsSettingsOnlySchemaVersion(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"1","default_timeout":"45s"}`)
	if _, err := loadWith(t, Options{}, cfgHome); err == nil {
		t.Fatalf("schema_version 1 must be rejected")
	}
}

// TestLoadRejectsWorkspacePathInProject locks the invariant that gives the ID
// its meaning: the workspace path is derived, never stored, so a document that
// tries to pin a project to an arbitrary guest path is not a config Torio can
// read at all.
func TestLoadRejectsWorkspacePathInProject(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"2","projects":[{"id":"my-project",`+
		`"display_name":"My Project","remote":"https://github.com/owner/my-project.git",`+
		`"path":"/home/hermes/projects/my-project"}]}`)
	if _, err := loadWith(t, Options{}, cfgHome); err == nil {
		t.Fatalf("a project carrying a workspace path must be rejected (fail closed)")
	}
}

// TestLoadRejectsInvalidProjectInV2Document proves registry validation is part
// of loading, not only of writing: a hand-edited document cannot smuggle in an
// entry the write path would have refused.
func TestLoadRejectsInvalidProjectInV2Document(t *testing.T) {
	for _, projects := range []string{
		`[{"id":"My-Project","display_name":"My Project","remote":"https://github.com/owner/repo.git"}]`,
		`[{"id":"my-project","display_name":"","remote":"https://github.com/owner/repo.git"}]`,
		`[{"id":"my-project","display_name":"My Project","remote":"/srv/git/repo.git"}]`,
		`[{"id":"my-project","display_name":"My Project","remote":"https://u:p@github.com/owner/repo.git"}]`,
		`[{"id":"a","display_name":"A","remote":"https://github.com/owner/a.git"},` +
			`{"id":"a","display_name":"A again","remote":"https://github.com/owner/b.git"}]`,
	} {
		cfgHome := t.TempDir()
		writeConfig(t, cfgHome, `{"schema_version":"2","projects":`+projects+`}`)
		if _, err := loadWith(t, Options{}, cfgHome); err == nil {
			t.Errorf("invalid registry %s must be rejected on load", projects)
		}
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{not json`)
	if _, err := loadWith(t, Options{}, cfgHome); err == nil {
		t.Fatalf("malformed JSON must be rejected")
	}
}

func TestLoadRejectsWrongSchemaVersion(t *testing.T) {
	for _, body := range []string{
		`{"schema_version":"5"}`,
		`{"schema_version":"0"}`,
		`{"schema_version":"v2"}`,
	} {
		cfgHome := t.TempDir()
		writeConfig(t, cfgHome, body)
		if _, err := loadWith(t, Options{}, cfgHome); err == nil {
			t.Errorf("unknown schema_version in %q must be rejected", body)
		}
	}
}

func TestLoadRejectsMissingSchemaVersion(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"default_timeout":"10s"}`)
	if _, err := loadWith(t, Options{}, cfgHome); err == nil {
		t.Fatalf("missing schema_version must be rejected")
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"2","surprise":true}`)
	if _, err := loadWith(t, Options{}, cfgHome); err == nil {
		t.Fatalf("unknown field must be rejected (fail closed)")
	}
}

func TestLoadRejectsSemanticallyInvalidTimeout(t *testing.T) {
	for _, body := range []string{
		`{"schema_version":"2","default_timeout":"-5s"}`,
		`{"schema_version":"2","default_timeout":"999h"}`,
		`{"schema_version":"2","default_timeout":"not-a-duration"}`,
		`{"schema_version":"2","default_timeout":"0s"}`,
	} {
		cfgHome := t.TempDir()
		writeConfig(t, cfgHome, body)
		if _, err := loadWith(t, Options{}, cfgHome); err == nil {
			t.Errorf("semantically invalid config %q must be rejected", body)
		}
	}
}

// TestLoadRejectsTrailingBytesAndSecondDocument locks strict single-document
// parsing: exactly one top-level JSON value, no trailing bytes. This covers the
// cases a bare Decoder.More() check misses — a trailing closing delimiter can
// make More() report false despite invalid remaining bytes.
func TestLoadRejectsTrailingBytesAndSecondDocument(t *testing.T) {
	for _, body := range []string{
		`{"schema_version":"2"}}`,                      // trailing }
		`{"schema_version":"2"}]`,                      // trailing ]
		`{"schema_version":"2"} trailing`,              // trailing garbage
		`{"schema_version":"2"}{"schema_version":"2"}`, // second document
	} {
		cfgHome := t.TempDir()
		writeConfig(t, cfgHome, body)
		if _, err := loadWith(t, Options{}, cfgHome); err == nil {
			t.Errorf("body %q must be rejected (exactly one JSON document)", body)
		}
	}
}

// secretCanary is a synthetic, matcher-valid fake GitHub token: it is not a
// real credential but DOES match the production redactor's gh[pousr]_ shape
// (24 alphanumerics after the prefix, so it satisfies the {20,} quantifier).
// Using a matcher-valid fixture is what makes the secret-rejection tests
// meaningful — see TestSecretCanaryIsRecognizedByProductionMatcher.
const secretCanary = "ghp_ABCDEFGHIJKLMNOPQRSTUVWX"

// TestSecretCanaryIsRecognizedByProductionMatcher guards the fixture: if the
// canary did not actually match the production redactor, the rejection tests
// below could pass for the wrong reason (e.g. duration validation) and the
// secret-rejection evidence would be false. This asserts the canary is real.
func TestSecretCanaryIsRecognizedByProductionMatcher(t *testing.T) {
	if !containsSecretShape(secretCanary) {
		t.Fatalf("canary %q is not recognized by the production redactor; fixture is invalid", secretCanary)
	}
}

// TestLoadRejectsSecretShapedValueWithoutLeaking proves config refuses
// secret-shaped material specifically via the secret detector (not incidental
// duration validation), and that the error text never echoes the secret.
func TestLoadRejectsSecretShapedValueWithoutLeaking(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"2","default_timeout":"`+secretCanary+`"}`)
	_, err := loadWith(t, Options{}, cfgHome)
	if err == nil {
		t.Fatalf("secret-shaped config value must be rejected")
	}
	// The rejection must be attributed to the secret detector, proving it is the
	// secret shape — not some other field validation — that fails closed.
	if !strings.Contains(err.Error(), "secret-shaped") {
		t.Errorf("rejection reason is not the secret detector: %q", err.Error())
	}
	if strings.Contains(err.Error(), secretCanary) {
		t.Errorf("error leaked the secret-shaped value: %q", err.Error())
	}
}

// escapedSecretRaw is the JSON-escaped wire form of secretCanary: the `h` in
// the gh[pousr]_ prefix is written as the h escape. On disk the raw bytes
// therefore contain no literal "ghp_", so a raw-byte pre-scan cannot see it —
// but encoding/json decodes it back into the exact matcher-valid secret. This
// is the escaping bypass the JSON-escaped no-leak tests exercise.
const escapedSecretRaw = "g\\u0068p_ABCDEFGHIJKLMNOPQRSTUVWX"

// TestEscapedSecretFixtureIsAGenuineBypass guards the escaped fixture: it must
// (a) NOT be caught by the raw-byte pre-scan in its on-disk form, yet (b) decode
// to the exact matcher-valid secret. If either invariant breaks, the escaped
// no-leak tests below could pass for the wrong reason.
func TestEscapedSecretFixtureIsAGenuineBypass(t *testing.T) {
	if containsSecretShape(escapedSecretRaw) {
		t.Fatalf("escaped fixture %q must NOT match the raw pre-scan (else it is not an escaping bypass)", escapedSecretRaw)
	}
	var got string
	if err := json.Unmarshal([]byte(`"`+escapedSecretRaw+`"`), &got); err != nil {
		t.Fatalf("unmarshal escaped fixture: %v", err)
	}
	if got != secretCanary {
		t.Fatalf("escaped fixture decodes to %q, want the canary %q", got, secretCanary)
	}
}

// TestLoadDoesNotLeakJSONEscapedSecretInAnyField proves the config API's own
// no-leak contract holds even when a matcher-valid secret is JSON-escaped so the
// raw pre-scan cannot see it. The decoder turns it back into a secret that could
// otherwise reach an error via %q interpolation (schema_version) or the
// DisallowUnknownFields decoder text (an escaped unknown field name). Every
// textual decoded surface is covered; the assertion is on the error returned by
// the config package itself, not the final CLI renderer.
func TestLoadDoesNotLeakJSONEscapedSecretInAnyField(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"schema_version", `{"schema_version":"` + escapedSecretRaw + `"}`},
		{"default_timeout", `{"schema_version":"2","default_timeout":"` + escapedSecretRaw + `"}`},
		{"unknown_field_name", `{"schema_version":"2","` + escapedSecretRaw + `":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgHome := t.TempDir()
			writeConfig(t, cfgHome, tc.body)
			_, err := loadWith(t, Options{}, cfgHome)
			if err == nil {
				t.Fatalf("JSON-escaped secret in %s must be rejected", tc.name)
			}
			if strings.Contains(err.Error(), secretCanary) {
				t.Errorf("config API error leaked JSON-escaped secret via %s: %q", tc.name, err.Error())
			}
		})
	}
}

// TestWriteFileWritesSortedV2Document locks the on-disk form: schema V2, the
// registry sorted by ID, and owner-only permissions. Sorting is what makes a
// write deterministic — the same registry always produces the same bytes,
// regardless of the order entries were added in.
func TestWriteFileWritesSortedV2Document(t *testing.T) {
	path := filepath.Join(t.TempDir(), appDir, configFileName)
	f := File{SchemaVersion: ConfigSchemaVersion, Timeout: 45 * time.Second, Projects: []Project{
		{ID: "zeta", DisplayName: "Zeta", Remote: "https://github.com/owner/zeta.git"},
		{ID: "alpha", DisplayName: "Alpha", Remote: "git@github.com:owner/alpha.git"},
	}}
	if err := WriteFile(path, f); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var raw fileJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("written document is not valid JSON: %v", err)
	}
	if raw.SchemaVersion != ConfigSchemaVersion {
		t.Errorf("written schema_version = %q, want %q", raw.SchemaVersion, ConfigSchemaVersion)
	}
	if raw.DefaultTimeout != "45s" {
		t.Errorf("written default_timeout = %q, want %q", raw.DefaultTimeout, "45s")
	}
	if len(raw.Projects) != 2 || raw.Projects[0].ID != "alpha" || raw.Projects[1].ID != "zeta" {
		t.Errorf("written projects = %+v, want sorted by id", raw.Projects)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := fi.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("config perm = %o, want owner-only", perm)
		}
	}
}

// TestWriteFileRoundTripsThroughLoad closes the loop: what the writer emits is
// exactly what the loader accepts, so a mutation cannot produce a document the
// next invocation refuses.
func TestWriteFileRoundTripsThroughLoad(t *testing.T) {
	cfgHome := t.TempDir()
	path := filepath.Join(cfgHome, appDir, configFileName)
	want := []Project{
		{ID: "alpha", DisplayName: "Alpha", Remote: "git@github.com:owner/alpha.git"},
		{ID: "zeta", DisplayName: "Zeta", Remote: "https://github.com/owner/zeta.git"},
	}
	if err := WriteFile(path, File{SchemaVersion: ConfigSchemaVersion, Projects: want}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rt, err := loadWith(t, Options{}, cfgHome)
	if err != nil {
		t.Fatalf("Load after WriteFile: %v", err)
	}
	if len(rt.File.Projects) != len(want) {
		t.Fatalf("Projects = %+v, want %+v", rt.File.Projects, want)
	}
	for i, p := range want {
		if rt.File.Projects[i] != p {
			t.Errorf("Projects[%d] = %+v, want %+v", i, rt.File.Projects[i], p)
		}
	}
}

// TestWriteFileRejectsInvalidDocumentBeforeCreatingFile keeps an invalid
// registry from ever reaching the disk — an atomic rename must not be what
// legitimizes a document nothing validated.
func TestWriteFileRejectsInvalidDocumentBeforeCreatingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), appDir, configFileName)
	bad := File{SchemaVersion: ConfigSchemaVersion, Projects: []Project{
		{ID: "ok", DisplayName: "Ok", Remote: "https://user:pass@github.com/owner/repo.git"},
	}}
	if err := WriteFile(path, bad); err == nil {
		t.Fatalf("invalid document must be rejected")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("rejected document must leave no file behind, stat err = %v", err)
	}
}

// TestWriteFileRequiresCurrentSchemaVersion pins the write gate: only the
// current version reaches disk, so no path can emit a document the next
// invocation would refuse to read. "2" and "3" are in the list on purpose — both
// are readable, and writing either back would silently drop a field a newer
// document declares: the backend for "2", the pinned operator key for "3".
func TestWriteFileRequiresCurrentSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), appDir, configFileName)
	for _, version := range []string{"", "1", "2", "3", "5"} {
		if err := WriteFile(path, File{SchemaVersion: version}); err == nil {
			t.Errorf("WriteFile with schema_version %q must be rejected", version)
		}
	}
}

// TestWriteFileRejectsUntrustedConfigDir pins constraint 4: an existing
// permissive directory must not become config authority just because the final
// rename is atomic.
func TestWriteFileRejectsUntrustedConfigDir(t *testing.T) {
	requireTrustPolicy(t)
	dir := filepath.Join(t.TempDir(), "torio")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := WriteFile(path, File{SchemaVersion: ConfigSchemaVersion}); err == nil {
		t.Fatalf("group/world-accessible config dir must be rejected")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("rejected write must leave no file behind, stat err = %v", err)
	}
}

// TestVerifyPersistedRejectsMismatchedDocument covers the post-write read-back:
// the bytes that landed on disk are parsed and validated again, and a document
// that is not the one we meant to persist is reported instead of trusted.
func TestVerifyPersistedRejectsMismatchedDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), appDir, configFileName)
	onDisk := File{SchemaVersion: ConfigSchemaVersion, Projects: []Project{
		{ID: "alpha", DisplayName: "Alpha", Remote: "https://github.com/owner/alpha.git"},
	}}
	if err := WriteFile(path, onDisk); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := verifyPersisted(path, onDisk); err != nil {
		t.Fatalf("matching document must verify: %v", err)
	}
	other := File{SchemaVersion: ConfigSchemaVersion, Projects: []Project{
		{ID: "zeta", DisplayName: "Zeta", Remote: "https://github.com/owner/zeta.git"},
	}}
	if err := verifyPersisted(path, other); err == nil {
		t.Fatalf("document that differs from the intended one must be reported")
	}
}

// TestDocumentIsRejectedByAPreRegistryReader is the forward-compatibility
// promise: a binary that predates the registry must refuse a document this
// binary writes rather than read it as settings-only and silently drop the
// projects. That reader no longer exists here, so the test replicates it — its
// exact wire struct, decoded strictly, behind its exact version gate — and
// feeds it real writer output.
func TestDocumentIsRejectedByAPreRegistryReader(t *testing.T) {
	// The settings-only wire form as a pre-registry binary declared it.
	type preRegistryJSON struct {
		SchemaVersion  string `json:"schema_version"`
		DefaultTimeout string `json:"default_timeout"`
	}

	path := filepath.Join(t.TempDir(), appDir, configFileName)
	if err := WriteFile(path, File{SchemaVersion: ConfigSchemaVersion, Projects: []Project{
		{ID: "alpha", DisplayName: "Alpha", Remote: "https://github.com/owner/alpha.git"},
	}}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var raw preRegistryJSON
	decodeErr := decodeStrict(data, &raw)
	if decodeErr == nil && raw.SchemaVersion == "1" {
		t.Fatalf("a pre-registry reader accepted a registry document")
	}
	// It must fail on the registry itself, not only on the version gate: even a
	// reader that was lax about the version cannot mistake the document.
	if decodeErr == nil {
		t.Errorf("pre-registry strict decode accepted the wire form; only the version gate rejected it")
	}
}

func TestWriteFileDoesNotLeakSecretShapedRemote(t *testing.T) {
	path := filepath.Join(t.TempDir(), appDir, configFileName)
	err := WriteFile(path, File{SchemaVersion: ConfigSchemaVersion, Projects: []Project{
		{ID: "alpha", DisplayName: "Alpha", Remote: "https://" + secretCanary + "@github.com/owner/alpha.git"},
	}})
	if err == nil {
		t.Fatalf("secret-shaped remote must be rejected")
	}
	if strings.Contains(err.Error(), secretCanary) {
		t.Errorf("error leaked the secret-shaped remote: %q", err.Error())
	}
}

func TestLoadRejectsInsecureConfigPermissions(t *testing.T) {
	requireTrustPolicy(t)
	cfgHome := t.TempDir()
	path := writeConfig(t, cfgHome, `{"schema_version":"2"}`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := loadWith(t, Options{}, cfgHome); err == nil {
		t.Fatalf("group/world-readable config must be rejected on Unix")
	}
}

// TestOperatorKeyRoundTrips proves the pin survives a write and a read back. It
// is the field that decides whether a session forwards one mediated key or the
// operator's whole agent, so losing it silently would widen the session rather
// than narrow it.
func TestOperatorKeyRoundTrips(t *testing.T) {
	const pin = "SHA256:453QtO4nWnVBB8P7WEvUS9HGshG6/XJgoa3Y3IKs+B4"
	path := filepath.Join(t.TempDir(), appDir, configFileName)
	if err := WriteFile(path, File{SchemaVersion: ConfigSchemaVersion, OperatorKey: pin}); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	got, err := parseFile(data)
	if err != nil {
		t.Fatalf("parseFile() error = %v", err)
	}
	if got.OperatorKey != pin {
		t.Errorf("OperatorKey = %q, want %q", got.OperatorKey, pin)
	}
}

// TestOperatorKeyIsRefusedByAnOlderDeclaredVersion holds the rule that a
// document means what its declared version says it means. A "3" document
// carrying operator_key would otherwise be read as pinning a key by a binary
// that also happily writes it back as "4".
func TestOperatorKeyIsRefusedByAnOlderDeclaredVersion(t *testing.T) {
	for _, body := range []string{
		`{"schema_version":"3","operator_key":"SHA256:abc"}`,
		`{"schema_version":"2","operator_key":"SHA256:abc"}`,
	} {
		cfgHome := t.TempDir()
		writeConfig(t, cfgHome, body)
		if _, err := loadWith(t, Options{}, cfgHome); err == nil {
			t.Errorf("operator_key in %q must be rejected", body)
		}
	}
}

// TestVersionThreeReadsAsCurrentWithNoPin: a document that predates the field is
// one whose sessions forwarded the operator's whole agent, which is exactly what
// an empty pin still means.
func TestVersionThreeReadsAsCurrentWithNoPin(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"3","backend":"claude-code","default_timeout":"45s"}`)
	rt, err := loadWith(t, Options{}, cfgHome)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if rt.File.SchemaVersion != ConfigSchemaVersion {
		t.Errorf("SchemaVersion = %q, want normalized to %q", rt.File.SchemaVersion, ConfigSchemaVersion)
	}
	if rt.File.Backend != "claude-code" {
		t.Errorf("Backend = %q, want the document's", rt.File.Backend)
	}
	if rt.File.OperatorKey != "" {
		t.Errorf("OperatorKey = %q, want empty", rt.File.OperatorKey)
	}
}

func TestOperatorKeyShapeIsChecked(t *testing.T) {
	for name, pin := range map[string]string{
		"control character": "SHA256:abc\x00def",
		"escape sequence":   "SHA256:abc\x1b[31m",
		"leading space":     " SHA256:abc",
		"trailing newline":  "SHA256:abc\n",
		"too long":          strings.Repeat("k", maxOperatorKeyLen+1),
	} {
		if err := (File{SchemaVersion: ConfigSchemaVersion, OperatorKey: pin}).Validate(); err == nil {
			t.Errorf("%s: operator_key %q must be rejected", name, pin)
		}
	}
	for name, pin := range map[string]string{
		"fingerprint": "SHA256:453QtO4nWnVBB8P7WEvUS9HGshG6/XJgoa3Y3IKs+B4",
		"comment":     "wiktor@mac",
		"unset":       "",
	} {
		if err := (File{SchemaVersion: ConfigSchemaVersion, OperatorKey: pin}).Validate(); err != nil {
			t.Errorf("%s: operator_key %q must be accepted, got %v", name, pin, err)
		}
	}
}
