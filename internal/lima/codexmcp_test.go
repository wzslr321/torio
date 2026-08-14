package lima

import (
	"strings"
	"testing"
)

// TestCodexRequirementsPinTheRelayAndItsExactArgument is the boundary this
// backend's MCP story rests on. The declaration is the agent's own file, so what
// decides whether a declared server may run is this allowlist, and it has to name
// the executable and the one argument rather than the executable alone.
func TestCodexRequirementsPinTheRelayAndItsExactArgument(t *testing.T) {
	grant := PolicyGrant{Services: []PolicyService{{Name: "atlassian"}, {Name: "slack"}}}
	doc := renderCodexRequirements(grant)

	for _, want := range []string{
		`[mcp_servers."atlassian"]`,
		`[mcp_servers."slack"]`,
		`executable = "` + TorioMCPRelayPath + `"`,
		`{ match = "exact", value = "atlassian" }`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the rendered allowlist does not contain %s:\n%s", want, doc)
		}
	}

	if err := codexRequirementsExact(doc, grant); err != nil {
		t.Fatalf("the allowlist Torio renders was rejected by its own verifier: %v", err)
	}
}

// TestAnAllowlistThatDoesNotMatchThePolicyIsRejected pins that verification is
// byte-exact. Torio renders this file, so anything else at that path is either a
// grant that moved or somebody editing the guest, and neither should read as
// configured.
func TestAnAllowlistThatDoesNotMatchThePolicyIsRejected(t *testing.T) {
	grant := PolicyGrant{Services: []PolicyService{{Name: "atlassian"}}}

	for _, tc := range []struct{ name, doc string }{
		{"a service the policy does not grant", renderCodexRequirements(PolicyGrant{Services: []PolicyService{{Name: "linear"}}})},
		{"an extra service", renderCodexRequirements(PolicyGrant{Services: []PolicyService{{Name: "atlassian"}, {Name: "linear"}}})},
		{"no allowlist at all", ""},
		{"a hand-written entry pointing somewhere else", `[mcp_servers."atlassian"]
identity = { command = { executable = "/usr/bin/env", args = [{ match = "exact", value = "atlassian" }] } }
`},
		{"an entry that pins the executable but not the argument", `[mcp_servers."atlassian"]
identity = { command = "` + TorioMCPRelayPath + `" }
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := codexRequirementsExact(tc.doc, grant); err == nil {
				t.Fatalf("an allowlist outside the policy was accepted:\n%s", tc.doc)
			}
		})
	}
}

// TestAnEmptyGrantStillWritesAnAllowlist is the case that decides what a box
// with no services does. An absent table leaves every declaration unconstrained;
// a present and empty one disables all of them, which is what a box that was
// granted nothing should do.
func TestAnEmptyGrantStillWritesAnAllowlist(t *testing.T) {
	doc := renderCodexRequirements(PolicyGrant{})

	if !strings.Contains(doc, "[mcp_servers]") {
		t.Fatalf("a box with no granted service writes no allowlist, so nothing is disabled:\n%s", doc)
	}
	if strings.Contains(doc, "[mcp_servers.") {
		t.Errorf("an empty grant rendered a server entry:\n%s", doc)
	}
	if err := codexRequirementsExact(doc, PolicyGrant{}); err != nil {
		t.Fatalf("the empty allowlist was rejected by its own verifier: %v", err)
	}
}

// TestWhatCodexResolvedMustBeThePolicyThroughTheRelay reads the listing rather
// than the agent's file, because the listing is what Codex actually resolved
// across every configuration layer and it carries whether the allowlist let the
// entry live. Each rejection below is a different way a box could look configured
// and not be.
func TestWhatCodexResolvedMustBeThePolicyThroughTheRelay(t *testing.T) {
	grant := PolicyGrant{Services: []PolicyService{{Name: "atlassian"}}}

	good := `[{"name":"atlassian","enabled":true,"transport":{"type":"stdio","command":"` +
		TorioMCPRelayPath + `","args":["atlassian"]}}]`
	entries, err := parseCodexMCPList(good)
	if err != nil {
		t.Fatalf("the listing a configured guest prints was not readable: %v", err)
	}
	if err := codexEntriesExact(entries, grant); err != nil {
		t.Fatalf("an exactly configured guest was rejected: %v", err)
	}

	for _, tc := range []struct{ name, doc string }{
		{"disabled by the allowlist", `[{"name":"atlassian","enabled":false,"transport":{"type":"stdio","command":"` +
			TorioMCPRelayPath + `","args":["atlassian"]}}]`},
		{"a direct remote server", `[{"name":"atlassian","enabled":true,"transport":{"type":"streamable_http","url":"https://example.test"}}]`},
		{"the relay with somebody else's argument", `[{"name":"atlassian","enabled":true,"transport":{"type":"stdio","command":"` +
			TorioMCPRelayPath + `","args":["linear"]}}]`},
		{"a command that is not the relay", `[{"name":"atlassian","enabled":true,"transport":{"type":"stdio","command":"/usr/bin/env","args":["atlassian"]}}]`},
		{"an extra server beside the granted one", `[{"name":"atlassian","enabled":true,"transport":{"type":"stdio","command":"` +
			TorioMCPRelayPath + `","args":["atlassian"]}},{"name":"linear","enabled":true,"transport":{"type":"stdio","command":"` +
			TorioMCPRelayPath + `","args":["linear"]}}]`},
		{"nothing configured", `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := parseCodexMCPList(tc.doc)
			if err != nil {
				t.Fatalf("listing did not parse: %v", err)
			}
			if err := codexEntriesExact(entries, grant); err == nil {
				t.Fatalf("a guest outside the policy was accepted:\n%s", tc.doc)
			}
		})
	}
}

// TestAnUnreadableListingIsNotAnEmptyOne pins that a listing Torio cannot parse
// fails rather than reading as a box with no servers, which is the answer that
// would pass every check above.
func TestAnUnreadableListingIsNotAnEmptyOne(t *testing.T) {
	for _, doc := range []string{"", "not json", `{"name":"atlassian"}`, `[] {"trailing":true}`} {
		if _, err := parseCodexMCPList(doc); err == nil {
			t.Errorf("%q was read as a valid listing", doc)
		}
	}
}

// TestServiceNamesAreQuotedInTheTableHeader pins the one shape a service name
// can take that plain TOML would read as two keys. Service names are validated
// to allow a dot, and an unquoted dot nests a table.
func TestServiceNamesAreQuotedInTheTableHeader(t *testing.T) {
	doc := renderCodexRequirements(PolicyGrant{Services: []PolicyService{{Name: "atlassian.cloud"}}})
	if !strings.Contains(doc, `[mcp_servers."atlassian.cloud"]`) {
		t.Errorf("a dotted service name was not quoted, so it names a nested table:\n%s", doc)
	}
}
