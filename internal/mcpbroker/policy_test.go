package mcpbroker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePolicy drops one policy document into dir under the given file name and
// returns the directory, so a test can describe a whole policy directory inline.
func writePolicy(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write policy %s: %v", name, err)
	}
}

// atlassianPolicy is the reference document: one read tool and one write tool,
// the shape ADR-0004 requires an operator to have written down by hand.
const atlassianPolicy = `{
  "schema_version": "1",
  "service": "atlassian",
  "upstream_endpoint": "https://api.atlassian.com/v1/mcp",
  "tools": [
    {"name": "getJiraIssue", "writes": false},
    {"name": "createJiraIssue", "writes": true}
  ]
}`

func TestLoadReadsAServicePolicy(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", atlassianPolicy)

	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	g := set.Grants()
	if len(g.Services) != 1 {
		t.Fatalf("Grants().Services = %+v, want exactly one service", g.Services)
	}
	svc := g.Services[0]
	if svc.Name != "atlassian" {
		t.Errorf("service name = %q, want %q", svc.Name, "atlassian")
	}
	if svc.UpstreamEndpoint != "https://api.atlassian.com/v1/mcp" {
		t.Errorf("upstream endpoint = %q, want the endpoint the document names", svc.UpstreamEndpoint)
	}
	if len(svc.Tools) != 2 {
		t.Fatalf("tools = %+v, want two", svc.Tools)
	}
	if svc.WriteTools != 1 {
		t.Errorf("WriteTools = %d, want 1", svc.WriteTools)
	}
}

func TestParseDocumentsUsesTheSameStrictPolicyContractAsLoad(t *testing.T) {
	set, err := ParseDocuments(map[string][]byte{"atlassian.json": []byte(atlassianPolicy)})
	if err != nil {
		t.Fatalf("ParseDocuments: %v", err)
	}
	grants := set.Grants()
	if len(grants.Services) != 1 || grants.Services[0].Name != "atlassian" || grants.Services[0].WriteTools != 1 {
		t.Fatalf("unexpected grants: %+v", grants)
	}
	if _, err := ParseDocuments(map[string][]byte{"atlassian.json": []byte(`{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://api.atlassian.com/v1/mcp","tools":[],"unknown":true}`)}); err == nil {
		t.Fatal("ParseDocuments accepted a policy with an unknown field")
	}
}

func TestPolicyDigestTracksTheEffectiveGrantNotJSONFormatting(t *testing.T) {
	first, err := ParseDocuments(map[string][]byte{
		"atlassian.json": []byte(`{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://api.atlassian.com/v1/mcp","tools":[{"name":"read","writes":false},{"name":"write","writes":true}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	reformatted, err := ParseDocuments(map[string][]byte{
		"atlassian.json": []byte("{\n  \"tools\": [{\"writes\": true, \"name\": \"write\"}, {\"writes\": false, \"name\": \"read\"}],\n  \"upstream_endpoint\": \"https://api.atlassian.com/v1/mcp\",\n  \"service\": \"atlassian\",\n  \"schema_version\": \"1\"\n}"),
	})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := ParseDocuments(map[string][]byte{
		"atlassian.json": []byte(`{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://api.atlassian.com/v1/mcp","tools":[{"name":"read","writes":false}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	if first.Digest() != reformatted.Digest() {
		t.Fatal("formatting or tool order changed the effective-policy digest")
	}
	if first.Digest() == changed.Digest() {
		t.Fatal("removing a granted tool did not change the effective-policy digest")
	}
}

// TestLoadRejectsUnknownField locks the fail-closed decoder. A field the loader
// does not understand is a grant somebody believes they wrote; accepting the
// document while ignoring the field would make the policy report a lie.
func TestLoadRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", `{
	  "schema_version": "1",
	  "service": "atlassian",
	  "upstream_endpoint": "https://api.atlassian.com/v1/mcp",
	  "tools": [{"name": "getJiraIssue", "writes": false}],
	  "allow_all": true
	}`)

	if _, err := Load(dir); err == nil {
		t.Fatalf("a document with an unknown field must be rejected")
	}
}

// TestLoadRejectsUnsupportedSchemaVersion locks the version gate. There is
// exactly one supported version and no migration path: a document written for a
// schema this binary does not implement is refused, never read on a best-effort
// basis, because a partially understood grant is not a grant anyone signed.
func TestLoadRejectsUnsupportedSchemaVersion(t *testing.T) {
	for _, version := range []string{"", "0", "2", "1.0"} {
		t.Run("version="+version, func(t *testing.T) {
			dir := t.TempDir()
			writePolicy(t, dir, "atlassian.json", `{
			  "schema_version": "`+version+`",
			  "service": "atlassian",
			  "upstream_endpoint": "https://api.atlassian.com/v1/mcp",
			  "tools": []
			}`)

			if _, err := Load(dir); err == nil {
				t.Fatalf("schema_version %q must be rejected", version)
			}
		})
	}
}

// TestLoadRejectsServiceFilenameMismatch locks the one identity a service has.
// The file stem is what an operator reads when auditing the directory and the
// service field is what the broker matches a connection against; if they can
// disagree, `slack.json` can quietly hold the Jira grant. Neither is preferred
// over the other — the disagreement itself is the error.
func TestLoadRejectsServiceFilenameMismatch(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "slack.json", atlassianPolicy)

	if _, err := Load(dir); err == nil {
		t.Fatalf("a service name that disagrees with its file name must be rejected")
	}
}

// TestLoadRejectsUnsupportedServiceName bounds the one identifier that leaves
// this package. ADR-0004 derives the broker's socket path from the service name
// (/run/torio-mcp/<service>.sock) and Hermes names the service in its own
// config, so nothing in this charset may traverse a directory, be re-read as an
// argument, or carry a terminal escape into an operator's report.
func TestLoadRejectsUnsupportedServiceName(t *testing.T) {
	for name, file := range map[string]string{
		"dot":         "atlassian.v2.json",
		"hiddenFile":  "..json",
		"uppercase":   "Atlassian.json",
		"underscore":  "atlassian_mcp.json",
		"leadingDash": "-atlassian.json",
		"space":       "atlassian mcp.json",
		"empty":       ".json",
		"tooLong":     strings.Repeat("a", MaxServiceNameLen+1) + ".json",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			stem := strings.TrimSuffix(file, ".json")
			writePolicy(t, dir, file, `{
			  "schema_version": "1",
			  "service": "`+stem+`",
			  "upstream_endpoint": "https://api.atlassian.com/v1/mcp",
			  "tools": []
			}`)

			if _, err := Load(dir); err == nil {
				t.Fatalf("service name %q must be rejected", stem)
			}
		})
	}
}

// TestLoadRejectsDuplicateToolName refuses a document that grants the same tool
// twice. The two entries can disagree on the write classification, and whichever
// one a map happened to keep would decide it — so the count of granted write
// tools would depend on decode order. Rejecting the document keeps the report
// and the enforcement the same thing.
func TestLoadRejectsDuplicateToolName(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", `{
	  "schema_version": "1",
	  "service": "atlassian",
	  "upstream_endpoint": "https://api.atlassian.com/v1/mcp",
	  "tools": [
	    {"name": "getJiraIssue", "writes": false},
	    {"name": "getJiraIssue", "writes": true}
	  ]
	}`)

	if _, err := Load(dir); err == nil {
		t.Fatalf("a duplicate tool name must be rejected")
	}
}

// TestLoadRejectsUnsupportedToolName bounds the tool name. A granted name is
// compared byte-for-byte against what a caller asks for and is echoed into every
// audit line, so a name carrying a newline, a terminal escape or an unbounded
// run of bytes is refused at the document, where an operator can still fix it.
//
// The control cases are written as JSON escapes on purpose: that is how such a
// byte reaches the decoder in practice, since the raw form would not be legal
// JSON in the first place.
func TestLoadRejectsUnsupportedToolName(t *testing.T) {
	for name, tool := range map[string]string{
		"empty":    "",
		"newline":  `getJira\nIssue`,
		"escape":   `getJira\u001b[2KIssue`,
		"space":    "get Jira Issue",
		"slash":    "jira/getIssue",
		"colon":    "jira:getIssue",
		"wildcard": "getJira*",
		"tooLong":  strings.Repeat("a", maxToolNameLen+1),
		"leadDash": "-getJiraIssue",
		"nonASCII": `getJiraIssuе`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writePolicy(t, dir, "atlassian.json", `{
			  "schema_version": "1",
			  "service": "atlassian",
			  "upstream_endpoint": "https://api.atlassian.com/v1/mcp",
			  "tools": [{"name": "`+tool+`", "writes": false}]
			}`)

			if _, err := Load(dir); err == nil {
				t.Fatalf("tool name %q must be rejected", tool)
			}
		})
	}
}

// TestLoadAcceptsToolNamesUpstreamActuallyUses guards against a charset so
// narrow it rejects real MCP servers: Atlassian names tools in camelCase, Slack
// uses snake_case, and namespaced servers use dots.
func TestLoadAcceptsToolNamesUpstreamActuallyUses(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", `{
	  "schema_version": "1",
	  "service": "atlassian",
	  "upstream_endpoint": "https://api.atlassian.com/v1/mcp",
	  "tools": [
	    {"name": "getJiraIssue", "writes": false},
	    {"name": "slack_send_message", "writes": true},
	    {"name": "atlassian.search", "writes": false},
	    {"name": "search-v2", "writes": false}
	  ]
	}`)

	if _, err := Load(dir); err != nil {
		t.Fatalf("Load must accept the tool names upstream servers actually use: %v", err)
	}
}

// TestLoadRejectsToolWithoutWriteClassification is the rule that keeps the
// transparency requirement honest. Go would read an omitted "writes" as false,
// so a forgotten field would silently reclassify a write tool as read-only and
// the granted-write count ADR-0004 requires would under-report. The
// classification is a claim someone has to make.
func TestLoadRejectsToolWithoutWriteClassification(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", `{
	  "schema_version": "1",
	  "service": "atlassian",
	  "upstream_endpoint": "https://api.atlassian.com/v1/mcp",
	  "tools": [{"name": "createJiraIssue"}]
	}`)

	if _, err := Load(dir); err == nil {
		t.Fatalf("a tool that does not declare \"writes\" must be rejected")
	}
}

// TestLoadRejectsAbsentToolList applies the same rule to the grant as a whole.
// Granting nothing is legal and meaningful, but it is a decision — an absent key
// is a document someone did not finish writing.
func TestLoadRejectsAbsentToolList(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", `{
	  "schema_version": "1",
	  "service": "atlassian",
	  "upstream_endpoint": "https://api.atlassian.com/v1/mcp"
	}`)

	if _, err := Load(dir); err == nil {
		t.Fatalf("a document without a \"tools\" key must be rejected")
	}
}

// TestLoadAcceptsEmptyToolList locks the other half: a service that is connected
// and granted nothing is a valid, useful state — the broker will speak for it and
// allow no call.
func TestLoadAcceptsEmptyToolList(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", `{
	  "schema_version": "1",
	  "service": "atlassian",
	  "upstream_endpoint": "https://api.atlassian.com/v1/mcp",
	  "tools": []
	}`)

	set, err := Load(dir)
	if err != nil {
		t.Fatalf("an empty grant is legal: %v", err)
	}
	g := set.Grants()
	if len(g.Services) != 1 {
		t.Fatalf("Services = %+v, want the service to be present", g.Services)
	}
	if len(g.Services[0].Tools) != 0 || g.Services[0].WriteTools != 0 {
		t.Errorf("grant = %+v, want no tools and no write tools", g.Services[0])
	}
}

// TestLoadRejectsUnsupportedUpstreamEndpoint guards the field ADR-0004 §8 made
// configurable. Two things must hold at once: a later egress decision has to be
// able to point it at a proxy, and this world-readable file must not become the
// place a token is kept. So the scheme set is small and userinfo, query and
// fragment — the three places a credential hides in a URL — are refused outright.
func TestLoadRejectsUnsupportedUpstreamEndpoint(t *testing.T) {
	for name, endpoint := range map[string]string{
		"empty":       "",
		"relative":    "/v1/mcp",
		"noScheme":    "api.atlassian.com/v1/mcp",
		"file":        "file:///etc/passwd",
		"userinfo":    "https://user@api.atlassian.com/v1/mcp",
		"credentials": "https://user:hunter2@api.atlassian.com/v1/mcp",
		"query":       "https://api.atlassian.com/v1/mcp?token=abc",
		"fragment":    "https://api.atlassian.com/v1/mcp#token",
		"noHost":      "https:///v1/mcp",
		"tooLong":     "https://api.atlassian.com/" + strings.Repeat("a", maxUpstreamLen),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writePolicy(t, dir, "atlassian.json", `{
			  "schema_version": "1",
			  "service": "atlassian",
			  "upstream_endpoint": "`+endpoint+`",
			  "tools": []
			}`)

			if _, err := Load(dir); err == nil {
				t.Fatalf("upstream_endpoint %q must be rejected", endpoint)
			}
		})
	}
}

// TestLoadAcceptsPlainHTTPUpstreamForALocalProxy is the deliberate exception.
// The egress-control decision ADR-0004 §8 anticipates would put a proxy in front
// of the broker, and requiring TLS to a loopback proxy would buy nothing and
// cost a certificate nobody would maintain.
func TestLoadAcceptsPlainHTTPUpstreamForALocalProxy(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", `{
	  "schema_version": "1",
	  "service": "atlassian",
	  "upstream_endpoint": "http://127.0.0.1:8080/atlassian",
	  "tools": []
	}`)

	if _, err := Load(dir); err != nil {
		t.Fatalf("a plain-HTTP egress proxy must be expressible: %v", err)
	}
}

// TestLoadRejectsTrailingData allows exactly one document per file. Anything
// after it is a second grant that would never be enforced, which is the worst
// possible outcome: an operator reads a file that says a tool is granted and the
// broker denies it, or the reverse.
func TestLoadRejectsTrailingData(t *testing.T) {
	for name, trailer := range map[string]string{
		"secondDocument": atlassianPolicy,
		"closingBrace":   "}",
		"junk":           "not json",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writePolicy(t, dir, "atlassian.json", atlassianPolicy+"\n"+trailer)

			if _, err := Load(dir); err == nil {
				t.Fatalf("trailing data after the document must be rejected")
			}
		})
	}
}

// TestDecodeStrictRejectsTrailingData pins the rule to the decoder rather than
// to the order parseDocument happens to do things in. Today the version probe
// rejects a second document first, because json.Unmarshal accepts exactly one;
// that is a side effect, and the "one document per file" contract must not
// depend on which check runs first.
func TestDecodeStrictRejectsTrailingData(t *testing.T) {
	var raw documentJSON
	if err := decodeStrict([]byte(atlassianPolicy+"\n"+atlassianPolicy), &raw); err == nil {
		t.Fatalf("decodeStrict must reject a second document")
	}
	if err := decodeStrict([]byte(atlassianPolicy+"\n"), &raw); err != nil {
		t.Errorf("trailing whitespace is not trailing data: %v", err)
	}
}

// TestLoadFailsWholeDirectoryOnOneBadDocument is the rule the rest of the
// failure modes rest on. A directory that half-parses must not produce a usable
// policy set: the surviving half would be a grant nobody wrote, and the broker
// would serve it while an operator read the file that failed and assumed it was
// in force.
func TestLoadFailsWholeDirectoryOnOneBadDocument(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", atlassianPolicy)
	writePolicy(t, dir, "slack.json", `{ this is not json`)

	set, err := Load(dir)
	if err == nil {
		t.Fatalf("one malformed document must fail the whole load")
	}
	if g := set.Grants(); len(g.Services) != 0 {
		t.Errorf("failed Load returned %d services, want a Set that grants nothing", len(g.Services))
	}
}

// TestLoadRejectsUnreadableDocument refuses to treat a document it cannot read
// as a document that grants nothing. Skipping it would silently revoke a grant
// on a permission glitch, and — far worse in the other direction — would let
// anyone who can make a file unreadable choose which policies apply.
func TestLoadRejectsUnreadableDocument(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a 0000 file regardless of its mode")
	}
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", atlassianPolicy)
	if err := os.Chmod(filepath.Join(dir, "atlassian.json"), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if _, err := Load(dir); err == nil {
		t.Fatalf("an unreadable policy document must fail the load")
	}
}

// TestLoadRejectsNonRegularEntry keeps policy authority where the ADR puts it.
// The directory is root-owned so its documents cannot be rewritten by the agent
// — but a symlink is a document whose *content* lives somewhere else entirely,
// and a link to a path under the agent's home would hand it the grant it is not
// supposed to be able to write. A directory or device node in the policy path is
// nonsense of the same family.
func TestLoadRejectsNonRegularEntry(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "elsewhere.json")
		if err := os.WriteFile(target, []byte(atlassianPolicy), 0o644); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "atlassian.json")); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		if _, err := Load(dir); err == nil {
			t.Fatalf("a symlinked policy document must be rejected")
		}
	})

	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "atlassian.json"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		if _, err := Load(dir); err == nil {
			t.Fatalf("a directory in the policy path must be rejected")
		}
	})
}

// TestLoadRejectsUnrecognisedFile refuses to quietly ignore what it does not
// read. A leftover `atlassian.yaml` or `atlassian.json.disabled` is a file
// somebody believes is in force; skipping it is how a stale document becomes an
// invisible grant — or an invisible revocation.
func TestLoadRejectsUnrecognisedFile(t *testing.T) {
	for _, name := range []string{"atlassian.yaml", "atlassian.json.disabled", "README", ".atlassian.json.swp"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writePolicy(t, dir, "atlassian.json", atlassianPolicy)
			writePolicy(t, dir, name, "whatever")

			err := loadErr(t, dir)
			if err == nil {
				t.Fatalf("%s must fail the load rather than be skipped", name)
			}
			// The message has to name the real problem. Without the extension gate
			// these files still fail — as malformed service names — and an operator
			// told their README is "not a lowercase slug" learns nothing.
			if !strings.Contains(err.Error(), "policy document") {
				t.Errorf("error = %v, want it to say the file is not a policy document", err)
			}
		})
	}
}

// loadErr loads dir and returns only the error, for tests that assert on the
// diagnosis rather than the outcome.
func loadErr(t *testing.T, dir string) error {
	t.Helper()
	_, err := Load(dir)
	return err
}

// TestLoadBoundsTheDirectory keeps the load cost of a policy directory bounded
// by policy rather than by whatever is on disk. The broker reloads this on
// start and on drift checks; nothing about "a few services, each with a listed
// set of tools" needs to be open-ended, and an open-ended read is the one an
// attacker who can write the directory would use.
func TestLoadBoundsTheDirectory(t *testing.T) {
	t.Run("services", func(t *testing.T) {
		dir := t.TempDir()
		for i := range maxServices + 1 {
			name := fmt.Sprintf("svc-%03d", i)
			writePolicy(t, dir, name+".json", `{
			  "schema_version": "1",
			  "service": "`+name+`",
			  "upstream_endpoint": "https://api.atlassian.com/v1/mcp",
			  "tools": []
			}`)
		}

		if _, err := Load(dir); err == nil {
			t.Fatalf("more than %d services must be rejected", maxServices)
		}
	})

	t.Run("tools", func(t *testing.T) {
		dir := t.TempDir()
		tools := make([]string, 0, maxToolsPerService+1)
		for i := range maxToolsPerService + 1 {
			tools = append(tools, fmt.Sprintf(`{"name": "tool%04d", "writes": false}`, i))
		}
		writePolicy(t, dir, "atlassian.json", `{
		  "schema_version": "1",
		  "service": "atlassian",
		  "upstream_endpoint": "https://api.atlassian.com/v1/mcp",
		  "tools": [`+strings.Join(tools, ",")+`]
		}`)

		if _, err := Load(dir); err == nil {
			t.Fatalf("more than %d tools in one service must be rejected", maxToolsPerService)
		}
	})

	t.Run("documentSize", func(t *testing.T) {
		dir := t.TempDir()
		writePolicy(t, dir, "atlassian.json", strings.Repeat(" ", maxDocumentBytes+1)+atlassianPolicy)

		if _, err := Load(dir); err == nil {
			t.Fatalf("a document larger than %d bytes must be rejected", maxDocumentBytes)
		}
	})
}

// TestLoadRejectsMissingDirectory separates "granted nothing" from "not
// installed". An absent policy directory would deny every call, which is safe —
// and exactly why it must be an error instead: a broker whose policy never
// loaded looks identical to one that was deliberately granted nothing, and the
// operator needs to be able to tell those apart.
func TestLoadRejectsMissingDirectory(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatalf("a missing policy directory must fail the load")
	}
}

// TestLoadAcceptsEmptyDirectory is the legal counterpart: the broker is
// installed and speaks for no service yet.
func TestLoadAcceptsEmptyDirectory(t *testing.T) {
	set, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("an empty policy directory is a valid state: %v", err)
	}
	if g := set.Grants(); len(g.Services) != 0 {
		t.Errorf("Services = %+v, want none", g.Services)
	}
}

// TestValidateServiceName pins the one rule that two packages depend on. The
// policy loader derives a service from a filename stem; cmd/torio-mcp-connect
// turns the same string into /run/torio-mcp/<service>.sock. A name accepted by
// one and rejected by the other is a socket nothing can reach, so the rule is
// exported and shared rather than written twice and hoped over.
func TestValidateServiceName(t *testing.T) {
	accepted := []string{"atlassian", "slack", "a", "a1", "jira-cloud", "x9-y8-z7"}
	for _, name := range accepted {
		if err := ValidateServiceName(name); err != nil {
			t.Errorf("ValidateServiceName(%q) = %v, want nil", name, err)
		}
	}

	rejected := map[string]string{
		"empty":        "",
		"uppercase":    "Atlassian",
		"traversal":    "../../etc/shadow",
		"separator":    "a/b",
		"leadingDash":  "-atlassian",
		"trailingDash": "atlassian-",
		"underscore":   "jira_cloud",
		"dot":          "jira.cloud",
		"space":        "jira cloud",
		"nul":          "jira\x00",
		"tooLong":      strings.Repeat("a", MaxServiceNameLen+1),
	}
	for label, name := range rejected {
		if err := ValidateServiceName(name); err == nil {
			t.Errorf("%s: ValidateServiceName(%q) = nil, want an error", label, name)
		}
	}

	// An over-long name must not be echoed back: the diagnostic would carry an
	// argv-sized string the caller chose.
	long := strings.Repeat("q", MaxServiceNameLen*4)
	err := ValidateServiceName(long)
	if err == nil {
		t.Fatal("over-long name accepted")
	}
	if strings.Contains(err.Error(), long) {
		t.Errorf("diagnostic echoes the over-long input: %v", err)
	}
}
