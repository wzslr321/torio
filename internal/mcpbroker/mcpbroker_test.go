package mcpbroker

import (
	"testing"
)

// loadReference builds the policy set the decision tests reason about: one
// service, one read tool, one write tool.
func loadReference(t *testing.T) Set {
	t.Helper()
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", atlassianPolicy)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return set
}

func TestAllowGrantsAListedTool(t *testing.T) {
	set := loadReference(t)

	for _, tool := range []string{"getJiraIssue", "createJiraIssue"} {
		d := set.Allow("atlassian", tool)
		if !d.Allowed {
			t.Errorf("Allow(atlassian, %s) = %+v, want allowed", tool, d)
		}
		if d.Reason != ReasonGranted {
			t.Errorf("Allow(atlassian, %s).Reason = %v, want ReasonGranted", tool, d.Reason)
		}
	}
}

// TestAllowDistinguishesUnknownServiceFromUnlistedTool separates the two
// denials. They look the same to the caller being denied and mean opposite
// things to the operator: an unknown service is a broker that was never
// configured for this connection, an unlisted tool is a service that was
// configured and deliberately not granted this.
func TestAllowDistinguishesUnknownServiceFromUnlistedTool(t *testing.T) {
	set := loadReference(t)

	unknown := set.Allow("slack", "slack_send_message")
	if unknown.Allowed || unknown.Reason != ReasonUnknownService {
		t.Errorf("Allow(slack, …) = %+v, want denied with ReasonUnknownService", unknown)
	}

	unlisted := set.Allow("atlassian", "deleteJiraProject")
	if unlisted.Allowed || unlisted.Reason != ReasonToolNotGranted {
		t.Errorf("Allow(atlassian, deleteJiraProject) = %+v, want denied with ReasonToolNotGranted", unlisted)
	}
}

// TestAllowMatchesNothingButAnExactName is the rule ADR-0022 §4 states and the
// one most likely to be eroded by a well-meaning change. Grants are enumerated
// by name: no prefix, no suffix, no glob, no case folding, no separator
// normalisation. Every input below is a way of "nearly" naming a granted tool.
func TestAllowMatchesNothingButAnExactName(t *testing.T) {
	set := loadReference(t)

	for _, tool := range []string{
		"getJiraIssue ",
		" getJiraIssue",
		"getJiraIssu",
		"getJiraIssues",
		"getjiraissue",
		"GETJIRAISSUE",
		"getJira*",
		"*",
		"",
		"get.JiraIssue",
		"get_Jira_Issue",
		"getJiraIssue\x00",
	} {
		if d := set.Allow("atlassian", tool); d.Allowed {
			t.Errorf("Allow(atlassian, %q) = %+v, want denied: only an exact name is granted", tool, d)
		}
	}

	// The same rule on the service side.
	for _, svc := range []string{"atlassia", "atlassians", "ATLASSIAN", "*", ""} {
		if d := set.Allow(svc, "getJiraIssue"); d.Allowed {
			t.Errorf("Allow(%q, getJiraIssue) = %+v, want denied", svc, d)
		}
	}
}

// TestZeroSetDeniesEverything locks the fail-closed zero value. A broker that
// has not loaded its policy, or whose load failed, holds exactly this — and it
// must not be able to allow anything.
func TestZeroSetDeniesEverything(t *testing.T) {
	var set Set

	d := set.Allow("atlassian", "getJiraIssue")
	if d.Allowed {
		t.Errorf("the zero Set allowed %+v; an unloaded policy must deny", d)
	}
	if d.Reason != ReasonUnknownService {
		t.Errorf("Reason = %v, want ReasonUnknownService: nothing is configured", d.Reason)
	}
	if g := set.Grants(); len(g.Services) != 0 {
		t.Errorf("Grants() = %+v, want empty", g)
	}
}

// TestGrantsEnumeratesTheWholePolicy is the transparency requirement in test
// form: every service, every granted tool, which of them write, and how many.
// `torio mcp status` renders this, so what it reports and what Allow enforces
// have to be the same thing.
func TestGrantsEnumeratesTheWholePolicy(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "atlassian.json", atlassianPolicy)
	writePolicy(t, dir, "slack.json", `{
	  "schema_version": "1",
	  "service": "slack",
	  "upstream_endpoint": "https://slack.com/api/mcp",
	  "tools": [
	    {"name": "slack_read_channel", "writes": false},
	    {"name": "slack_send_message", "writes": true},
	    {"name": "slack_add_reaction", "writes": true}
	  ]
	}`)
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	g := set.Grants()
	want := Grant{Services: []ServiceGrant{
		{
			Name:             "atlassian",
			UpstreamEndpoint: "https://api.atlassian.com/v1/mcp",
			Tools: []ToolGrant{
				{Name: "createJiraIssue", Writes: true},
				{Name: "getJiraIssue", Writes: false},
			},
			WriteTools: 1,
		},
		{
			Name:             "slack",
			UpstreamEndpoint: "https://slack.com/api/mcp",
			Tools: []ToolGrant{
				{Name: "slack_add_reaction", Writes: true},
				{Name: "slack_read_channel", Writes: false},
				{Name: "slack_send_message", Writes: true},
			},
			WriteTools: 2,
		},
	}}

	if len(g.Services) != len(want.Services) {
		t.Fatalf("Services = %+v, want %+v", g.Services, want.Services)
	}
	for i, ws := range want.Services {
		gs := g.Services[i]
		if gs.Name != ws.Name || gs.UpstreamEndpoint != ws.UpstreamEndpoint || gs.WriteTools != ws.WriteTools {
			t.Errorf("Services[%d] = %+v, want %+v", i, gs, ws)
		}
		if len(gs.Tools) != len(ws.Tools) {
			t.Fatalf("Services[%d].Tools = %+v, want %+v", i, gs.Tools, ws.Tools)
		}
		for j, wt := range ws.Tools {
			if gs.Tools[j] != wt {
				t.Errorf("Services[%d].Tools[%d] = %+v, want %+v", i, j, gs.Tools[j], wt)
			}
		}
	}
}

// TestGrantsCannotWidenThePolicy locks the value semantics. A report is
// something a caller holds and formats; if it shared memory with the Set, a
// formatting bug — or a caller that sorts in place, or reuses the slice — could
// change what the broker enforces without going anywhere near Load.
func TestGrantsCannotWidenThePolicy(t *testing.T) {
	set := loadReference(t)

	g := set.Grants()
	g.Services[0].Name = "slack"
	g.Services[0].Tools[0].Name = "deleteJiraProject"
	g.Services[0].Tools[0].Writes = true
	g.Services[0].WriteTools = 99

	if d := set.Allow("atlassian", "deleteJiraProject"); d.Allowed {
		t.Errorf("mutating a Grant granted a tool: %+v", d)
	}
	if d := set.Allow("slack", "getJiraIssue"); d.Allowed {
		t.Errorf("mutating a Grant renamed a service: %+v", d)
	}

	again := set.Grants()
	if again.Services[0].Name != "atlassian" || again.Services[0].WriteTools != 1 {
		t.Errorf("second Grants() = %+v, want the policy unchanged", again.Services[0])
	}
}
