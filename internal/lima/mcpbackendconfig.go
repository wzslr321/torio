package lima

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

const claudeManagedSettingsPath = "/etc/claude-code/managed-settings.json"

func hermesMCPConfigExact(doc string, grant PolicyGrant) error {
	scan, err := scanMCPServers(doc)
	if err != nil {
		return err
	}
	if scan.Foreign != 0 || len(scan.Entries) != len(grant.Services) {
		return fmt.Errorf("configured services do not exactly match policy")
	}
	for _, service := range grant.Services {
		entry, ok := scan.Entries[service.Name]
		if !ok || entry.Command != TorioMCPRelayPath || len(entry.Args) != 1 || entry.Args[0] != service.Name {
			return fmt.Errorf("configured service does not use its exact relay argument")
		}
	}
	return nil
}

type claudeManagedDocument struct {
	MCPServers map[string]claudeManagedServer `json:"mcpServers"`
}

type claudeManagedServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

func claudeManagedMCPExact(doc string, grant PolicyGrant) error {
	var config claudeManagedDocument
	dec := json.NewDecoder(bytes.NewBufferString(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&config); err != nil {
		return fmt.Errorf("managed MCP JSON is not the strict schema")
	}
	if err := ensureJSONDecoderEOF(dec); err != nil {
		return err
	}
	if len(config.MCPServers) != len(grant.Services) {
		return fmt.Errorf("managed MCP services do not exactly match policy")
	}
	for _, service := range grant.Services {
		entry, ok := config.MCPServers[service.Name]
		if !ok || entry.Type != "stdio" || entry.Command != TorioMCPRelayPath ||
			len(entry.Args) != 1 || entry.Args[0] != service.Name || len(entry.Env) != 0 {
			return fmt.Errorf("managed MCP service does not use its exact credential-free relay")
		}
	}
	return nil
}

func ensureJSONDecoderEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("managed MCP JSON has trailing content")
	}
	return nil
}

func claudeManagedOnly(doc string) bool {
	var config map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewBufferString(doc))
	if err := dec.Decode(&config); err != nil {
		return false
	}
	if ensureJSONDecoderEOF(dec) != nil {
		return false
	}
	value, ok := config["allowManagedMcpServersOnly"]
	if !ok {
		return false
	}
	var enabled bool
	return json.Unmarshal(value, &enabled) == nil && enabled
}

func claudeAgentMCPEmpty(doc string) bool {
	var config map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewBufferString(doc))
	if err := dec.Decode(&config); err != nil {
		return false
	}
	if ensureJSONDecoderEOF(dec) != nil {
		return false
	}
	if _, present := config["mcpServers"]; present {
		return false
	}
	var projects map[string]map[string]json.RawMessage
	if raw, present := config["projects"]; present {
		if json.Unmarshal(raw, &projects) != nil {
			return false
		}
		for _, project := range projects {
			if _, present := project["mcpServers"]; present {
				return false
			}
		}
	}
	return true
}

func (a *Adapter) verifyBackendMCPConfig(ctx context.Context, rep *MCPBrokerReport, backendName string) error {
	switch backendName {
	case "hermes":
		return a.verifyHermesMCPConfigForPolicy(ctx, rep)
	case "claude-code":
		return a.verifyClaudeManagedMCPConfig(ctx, rep)
	default:
		return a.brokerFailed(rep, "agent_mcp_config", "selected backend has no MCP configuration verifier", "use a supported backend")
	}
}

func (a *Adapter) verifyHermesMCPConfigForPolicy(ctx context.Context, rep *MCPBrokerReport) error {
	const name = hermesMCPServersCheck
	doc, err := a.readRegularRootFile(ctx, rep, name, HermesConfigPath, "the Hermes configuration")
	if err != nil {
		return err
	}
	if err := hermesMCPConfigExact(doc, rep.Policy); err != nil {
		return a.brokerFailed(rep, name, "Hermes MCP entries do not exactly match the root-owned policy",
			"run `torio mcp install`; this agent-writable file is a drift detector, not the authorization boundary")
	}
	rep.record(name, true, fmt.Sprintf("%d entr(ies), exact policy services through relay", len(rep.Policy.Services)))
	return nil
}

func (a *Adapter) verifyClaudeManagedMCPConfig(ctx context.Context, rep *MCPBrokerReport) error {
	const name = "claude_mcp_servers"
	settings, err := a.readTrustedRootFile(ctx, rep, name, claudeManagedSettingsPath)
	if err != nil {
		return err
	}
	if !claudeManagedOnly(settings) {
		return a.brokerFailed(rep, name, "Claude Code does not exclude unmanaged MCP servers",
			"run `torio vm bootstrap` to restore allowManagedMcpServersOnly")
	}
	doc, err := a.readTrustedRootFile(ctx, rep, name, ClaudeManagedMCPPath)
	if err != nil {
		return err
	}
	if err := claudeManagedMCPExact(doc, rep.Policy); err != nil {
		return a.brokerFailed(rep, name, "Claude managed MCP entries do not exactly match the root-owned policy",
			"run `torio mcp install` to restore the credential-free relay entries")
	}
	agentPath := "/home/" + rep.AgentUser + "/.claude.json"
	st, kind, err := a.statPath(ctx, rep, name, agentPath)
	if err != nil {
		return err
	}
	if st == pathUnprovable {
		return a.probeUnusable(rep, name, "the Claude agent-owned configuration")
	}
	if st == pathPresent {
		if kind != "regular file" {
			return a.brokerFailed(rep, name, "Claude agent-owned configuration is not a regular file", "inspect the guest configuration")
		}
		agentDoc, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "cat", agentPath)
		if err != nil {
			return err
		}
		if agentDoc.exit != 0 || !claudeAgentMCPEmpty(agentDoc.out) {
			return a.brokerFailed(rep, name, "Claude agent-owned configuration still declares a native MCP server",
				"run `torio mcp install`, then revoke any former native MCP credential upstream")
		}
	}
	rep.record(name, true, fmt.Sprintf("%d managed entr(ies), exact policy services through relay", len(rep.Policy.Services)))
	return nil
}

func (a *Adapter) readRegularRootFile(ctx context.Context, rep *MCPBrokerReport, name, filePath, subject string) (string, error) {
	st, kind, err := a.statPath(ctx, rep, name, filePath)
	if err != nil {
		return "", err
	}
	if st != pathPresent || kind != "regular file" {
		return "", a.brokerFailed(rep, name, subject+" is missing or not a regular file", "run `torio mcp install`")
	}
	doc, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "cat", filePath)
	if err != nil {
		return "", err
	}
	if doc.exit != 0 {
		return "", a.brokerFailed(rep, name, "could not read "+subject, "inspect the guest configuration")
	}
	return doc.out, nil
}

func (a *Adapter) readTrustedRootFile(ctx context.Context, rep *MCPBrokerReport, name, filePath string) (string, error) {
	metadata, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%F %U:%G %a", filePath)
	if err != nil {
		return "", err
	}
	if metadata.exit != 0 || metadata.trimmed() != "regular file root:root 644" {
		return "", a.brokerFailed(rep, name, "managed MCP configuration ownership or mode has drifted", "restore it as root:root 0644")
	}
	doc, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "cat", filePath)
	if err != nil {
		return "", err
	}
	if doc.exit != 0 {
		return "", a.brokerFailed(rep, name, "managed MCP configuration could not be read", "inspect the guest configuration")
	}
	return doc.out, nil
}
