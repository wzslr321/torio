package lima

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
)

// CodexRequirementsPath is Codex's admin requirements layer. It is root-owned,
// and it is where Torio writes the allowlist that decides which declared MCP
// server may run.
//
// This is the boundary, and it is a different shape from the other process
// backend's. There, Torio writes the server definitions into a root-owned file
// and a managed setting rejects everything else. Here the definitions stay in the
// agent's own configuration, written through Codex's own command, and this file
// decides which of them are permitted by matching the executable and every
// argument. The agent may therefore declare whatever it likes and change what it
// declared; what it cannot do is make Codex run anything this file does not name.
const CodexRequirementsPath = "/etc/codex/requirements.toml"

// renderCodexRequirements produces the allowlist for one grant, deterministically.
//
// Rendering happens here rather than on the guest because verification is byte
// equality against this function's output. Go has no TOML writer in its standard
// library and Torio does not carry one, and it does not need one: the document is
// this small, the service names are validated before they reach it, and a
// comparison that parses would accept a document that means the same thing to a
// parser nobody in this repository owns.
//
// A grant with no services still writes the table. An absent `mcp_servers` table
// constrains nothing, so a box granted no service would leave every declaration
// the agent makes permitted; an empty table permits none of them.
func renderCodexRequirements(grant PolicyGrant) string {
	var b strings.Builder
	b.WriteString("# Written by torio mcp install. This file is the allowlist Codex checks\n")
	b.WriteString("# a declared MCP server against, and it names the credential-free relay and\n")
	b.WriteString("# the single service argument each entry may carry. An empty table permits\n")
	b.WriteString("# no server at all.\n\n")

	if len(grant.Services) == 0 {
		b.WriteString("[mcp_servers]\n")
		return b.String()
	}
	for _, service := range grant.Services {
		fmt.Fprintf(&b, "[mcp_servers.%q]\n", service.Name)
		fmt.Fprintf(&b, "identity = { command = { executable = %q, args = [{ match = \"exact\", value = %q }] } }\n\n",
			TorioMCPRelayPath, service.Name)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// codexRequirementsExact reports whether the guest carries exactly the allowlist
// this grant renders to.
func codexRequirementsExact(doc string, grant PolicyGrant) error {
	if doc != renderCodexRequirements(grant) {
		return fmt.Errorf("the MCP allowlist is not the one this policy renders to")
	}
	return nil
}

// codexMCPEntry is one row of `codex mcp list --json`.
//
// The list is read rather than the agent's configuration file because it is what
// Codex resolved: it spans every configuration layer, and it carries whether the
// entry is enabled, which is the allowlist's answer rather than the agent's
// claim. Unknown fields are tolerated here, unlike the other backend's managed
// document, because this is a program's output across versions and not a file
// Torio wrote.
type codexMCPEntry struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Transport struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"transport"`
}

func parseCodexMCPList(doc string) ([]codexMCPEntry, error) {
	var entries []codexMCPEntry
	dec := json.NewDecoder(bytes.NewBufferString(doc))
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("the Codex MCP listing is not readable JSON")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("the Codex MCP listing has trailing content")
	}
	return entries, nil
}

// codexEntriesExact reports whether what Codex resolved is exactly the policy,
// reached through the credential-free relay, and permitted.
func codexEntriesExact(entries []codexMCPEntry, grant PolicyGrant) error {
	if len(entries) != len(grant.Services) {
		return fmt.Errorf("resolved services do not exactly match policy")
	}
	byName := make(map[string]codexMCPEntry, len(entries))
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	for _, service := range grant.Services {
		entry, ok := byName[service.Name]
		if !ok {
			return fmt.Errorf("a policy service is not configured")
		}
		if entry.Transport.Type != "stdio" || entry.Transport.Command != TorioMCPRelayPath ||
			len(entry.Transport.Args) != 1 || entry.Transport.Args[0] != service.Name {
			return fmt.Errorf("configured service does not use its exact credential-free relay")
		}
		if !entry.Enabled {
			return fmt.Errorf("a policy service is configured but not permitted to run")
		}
	}
	return nil
}

// reconcileCodexMCPConfig writes the root-owned allowlist and brings the agent's
// own declarations in line with it.
//
// The declarations are written through `codex mcp add` rather than by editing the
// agent's configuration from outside. Codex owns that file's format, it rewrites
// the file itself, and Go carries no TOML writer to argue with it. The allowlist
// is what makes this safe to delegate: a declaration Codex writes still has to
// match the root-owned entry before it will run.
func (a *Adapter) reconcileCodexMCPConfig(ctx context.Context, identity backend.Identity, grant PolicyGrant, rep *MCPBrokerInstallReport) (bool, error) {
	input := mcpClientConfigInput{
		Services: make([]string, 0, len(grant.Services)),
		Rendered: renderCodexRequirements(grant),
	}
	for _, service := range grant.Services {
		if err := ValidateServiceName(service.Name); err != nil {
			return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("policy service is invalid")}
		}
		input.Services = append(input.Services, service.Name)
	}
	body, err := json.Marshal(input)
	if err != nil {
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("MCP client configuration could not be rendered")}
	}
	res, err := a.SSHInput(ctx, body, []string{"sudo", "-n", "--", "/usr/bin/python3", "-c",
		reconcileMCPClientProgram, "codex", CodexRequirementsPath, TorioMCPRelayPath})
	if err != nil {
		return false, err
	}
	if res.StdoutTruncated || res.StderrTruncated || res.ExitCode != 0 {
		rep.record("install:agent_mcp_config", false, "allowlist reconcile failed")
		return false, &Error{Op: mcpInstallOp, Kind: KindCommandFailed, Err: fmt.Errorf("MCP allowlist reconcile exited %d", res.ExitCode)}
	}
	changed, ok := mcpConfigChanged(res.Stdout)
	if !ok {
		rep.record("install:agent_mcp_config", false, "allowlist result was not verifiable")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("MCP allowlist reconcile returned an unknown result")}
	}

	declarationsChanged, err := a.reconcileCodexDeclarations(ctx, identity, grant, rep)
	if err != nil {
		return changed, err
	}
	changed = changed || declarationsChanged

	if changed {
		rep.record("install:agent_mcp_config", true, identity.Name+" configured through relay")
		return true, nil
	}
	rep.record("install:agent_mcp_config", true, identity.Name+" already configured through relay")
	return false, nil
}

func (a *Adapter) reconcileCodexDeclarations(ctx context.Context, identity backend.Identity, grant PolicyGrant, rep *MCPBrokerInstallReport) (bool, error) {
	listed, err := a.codexMCPList(ctx, identity)
	if err != nil {
		rep.record("install:agent_mcp_config", false, "could not read the agent's MCP declarations")
		return false, err
	}
	if codexEntriesExact(listed, grant) == nil {
		return false, nil
	}

	// Every declaration goes, then the policy's are written fresh. Removing a
	// granted name before adding it is not redundant: `mcp add` is not a replace,
	// so a declaration pointing somewhere else has to stop existing rather than
	// gain a sibling. Removing an ungranted one matters for a different reason:
	// the allowlist already stops it running, and taking the name out as well
	// stops an operator reading a configured server as a granted one.
	for _, entry := range listed {
		// The names here come from the guest, not from policy. One that is not a
		// name Torio would ever write is not passed back as an argument: the run
		// stops and says so, because a reconcile that laundered an arbitrary
		// guest string into a command would be a worse outcome than a box an
		// operator has to look at.
		if err := ValidateServiceName(entry.Name); err != nil {
			rep.record("install:agent_mcp_config", false, "an agent declaration is not a name Torio writes")
			return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
				Err: fmt.Errorf("the agent declares an MCP server whose name Torio will not pass back to the guest; remove it with `codex mcp remove` in a `torio vm shell` session")}
		}
		if err := a.codexMCPCommand(ctx, identity, rep, "remove", entry.Name); err != nil {
			return true, err
		}
	}
	for _, service := range grant.Services {
		if err := a.codexMCPAdd(ctx, identity, rep, service.Name); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (a *Adapter) codexMCPList(ctx context.Context, identity backend.Identity) ([]codexMCPEntry, error) {
	res, err := a.SSH(ctx, []string{"sudo", "-n", "-u", identity.GuestUser, "-H", "--",
		"codex", "mcp", "list", "--json"})
	if err != nil {
		return nil, err
	}
	if res.StdoutTruncated || res.StderrTruncated || res.ExitCode != 0 {
		return nil, &Error{Op: mcpInstallOp, Kind: KindCommandFailed, Err: fmt.Errorf("`codex mcp list` exited %d", res.ExitCode)}
	}
	entries, err := parseCodexMCPList(string(res.Stdout))
	if err != nil {
		return nil, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: err}
	}
	return entries, nil
}

func (a *Adapter) codexMCPAdd(ctx context.Context, identity backend.Identity, rep *MCPBrokerInstallReport, service string) error {
	return a.codexMCPCommand(ctx, identity, rep, "add", service, "--", TorioMCPRelayPath, service)
}

func (a *Adapter) codexMCPCommand(ctx context.Context, identity backend.Identity, rep *MCPBrokerInstallReport, verb, service string, rest ...string) error {
	if err := ValidateServiceName(service); err != nil {
		return &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("policy service is invalid")}
	}
	argv := append([]string{"sudo", "-n", "-u", identity.GuestUser, "-H", "--", "codex", "mcp", verb, service}, rest...)
	res, err := a.SSH(ctx, argv)
	if err != nil {
		return err
	}
	if res.StdoutTruncated || res.StderrTruncated || res.ExitCode != 0 {
		rep.record("install:agent_mcp_config", false, "declaration reconcile failed")
		return &Error{Op: mcpInstallOp, Kind: KindCommandFailed, Err: fmt.Errorf("`codex mcp %s` exited %d", verb, res.ExitCode)}
	}
	return nil
}

func (a *Adapter) verifyCodexMCPConfig(ctx context.Context, rep *MCPBrokerReport) error {
	const name = "codex_mcp_servers"

	doc, err := a.readTrustedRootFile(ctx, rep, name, CodexRequirementsPath)
	if err != nil {
		return err
	}
	if err := codexRequirementsExact(doc, rep.Policy); err != nil {
		return a.brokerFailed(rep, name, "the Codex MCP allowlist does not exactly match the root-owned policy",
			"run `torio mcp install` to restore the allowlist that names the credential-free relay")
	}

	listed, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "-u", rep.AgentUser, "-H", "--",
		"codex", "mcp", "list", "--json")
	if err != nil {
		return err
	}
	if listed.exit != 0 {
		return a.probeUnusable(rep, name, "the Codex MCP listing")
	}
	entries, parseErr := parseCodexMCPList(listed.out)
	if parseErr != nil {
		return a.probeUnusable(rep, name, "the Codex MCP listing")
	}
	if err := codexEntriesExact(entries, rep.Policy); err != nil {
		// A declaration outside the policy is already stopped from running by the
		// allowlist, so this is not the route being open. It is failed because a
		// declaration Torio did not write can hold a credential the agent uid can
		// read, and that survives the server being disabled.
		return a.brokerFailed(rep, name, "Codex resolves MCP servers that do not exactly match the root-owned policy",
			"run `torio mcp install`, then revoke any former native MCP credential upstream")
	}
	rep.record(name, true, fmt.Sprintf("%d allowlisted entr(ies), exact policy services through relay", len(rep.Policy.Services)))
	return nil
}
