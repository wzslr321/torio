package lima

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// HermesConfigPath is the agent's own configuration. It is not on the Hermes
// write denylist and HERMES_WRITE_SAFE_ROOT is unset, so the identity the agent
// has a shell as can rewrite it — that is ADR-0004's own premise for why
// `mcp_servers.<n>.tools.include` is a default rather than a control.
//
// The same writability is a bypass: an entry naming any command other than the
// relay is an MCP server the broker never sees. No policy decides it, no audit
// line records it.
const HermesConfigPath = HermesProfilePath + "/config.yaml"

// TorioMCPRelayPath is the only command an mcp_servers entry may name on a
// managed guest (ADR-0004 §3). The relay itself holds no secret and is not a
// control — the agent may bypass it and talk to the socket directly, and nothing
// changes. What matters is that every configured server *ends up* at the broker.
const TorioMCPRelayPath = "/usr/local/bin/torio-mcp-connect"

// mcpServersKey is the top-level key the check reads.
const mcpServersKey = "mcp_servers"

// verifyHermesMCPServers proves no MCP server is configured that skips the
// broker.
//
// This is a drift check and cannot be more than that: the file it reads is
// writable by the party it constrains, so an agent that set out to defeat it
// could write YAML that Hermes and this reader disagree about. It is worth
// having anyway, for the same reason the token-store check is — it catches the
// realistic cause, which is a config that drifted or an `hermes mcp add` run by
// hand, not an adversary composing a parser differential.
func (a *Adapter) verifyHermesMCPServers(ctx context.Context, rep *MCPBrokerReport) error {
	const name = "hermes_mcp_servers"

	st, kind, err := a.statPath(ctx, rep, name, HermesConfigPath)
	if err != nil {
		return err
	}
	if st == pathUnprovable {
		return a.probeUnusable(rep, name, "the Hermes configuration")
	}
	if st == pathAbsent {
		// No config is no MCP server, and no MCP server cannot be a bypass.
		rep.record(name, true, "no Hermes configuration")
		return nil
	}
	if kind != "regular file" {
		return a.brokerFailed(rep, name, "the Hermes configuration is not a regular file",
			"inspect the guest by hand; a link here would put the agent's configuration somewhere nobody checked")
	}

	doc, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "cat", HermesConfigPath)
	if err != nil {
		return err
	}
	if doc.exit != 0 {
		return a.brokerFailed(rep, name, "could not read the Hermes configuration",
			"verify "+HermesConfigPath+" on the guest")
	}

	scan, err := scanMCPServers(doc.out)
	if err != nil {
		// The reason is a fixed sentence chosen from a closed set, never a
		// fragment of the file: this document is agent-written, and a report is
		// somewhere the agent must not be able to put text.
		return a.brokerFailed(rep, name, "the mcp_servers block could not be read with confidence: "+err.Error(),
			"the check reads one shape of YAML and refuses the rest; rewrite the block as plain nested keys or inspect it by hand")
	}
	if scan.Foreign > 0 {
		return a.brokerFailed(rep, name,
			fmt.Sprintf("%d of %d MCP server entries do not go through the broker relay", scan.Foreign, scan.Services),
			"every entry must run "+TorioMCPRelayPath+"; an entry naming anything else reaches its upstream with no policy or audit")
	}

	rep.record(name, true, fmt.Sprintf("%d entr(ies), all through the relay", scan.Services))
	return nil
}

// mcpServersScan is what the reader established about the block.
type mcpServersScan struct {
	Services int
	Foreign  int
}

// Refusals. Each is a fixed sentence, so a caller can render one without ever
// quoting the document that produced it.
var (
	errConfigTab       = errors.New("the document indents with tabs")
	errConfigMultiDoc  = errors.New("the document holds more than one YAML document")
	errConfigDuplicate = errors.New("the document declares mcp_servers more than once")
	errConfigInline    = errors.New("the block uses inline or flow syntax")
	errConfigAnchor    = errors.New("the block uses an anchor, alias or merge key")
	errConfigShape     = errors.New("the block is not a plain mapping of service names to settings")
	errConfigNoCommand = errors.New("a service entry names no command")
	errConfigTwoCmds   = errors.New("a service entry names a command twice")
)

// scanMCPServers reads the mcp_servers block and counts the entries that do not
// run the relay.
//
// It models exactly one shape — a block mapping of service names, each holding a
// scalar `command` — and returns an error for everything else. That is the point
// rather than a limitation: a config this reader cannot follow is a config it
// cannot vouch for, and "could not tell" has to reach the operator as the same
// answer as "no". A permissive reader would instead have to guess, and its guess
// would be the thing being verified.
func scanMCPServers(doc string) (mcpServersScan, error) {
	lines := strings.Split(doc, "\n")

	// A second document can redefine everything the first said, and the reader
	// would be reporting on the wrong one. A leading marker is ordinary and
	// carries no such risk, so it alone is allowed.
	seenContent := false
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "---" || trimmed == "..." {
			if seenContent {
				return mcpServersScan{}, errConfigMultiDoc
			}
			continue
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			seenContent = true
		}
	}

	start := -1
	for i, raw := range lines {
		if indentOf(raw) != 0 {
			continue
		}
		key, value, ok := splitYAMLEntry(raw)
		if !ok || key != mcpServersKey {
			continue
		}
		if start >= 0 {
			// YAML resolves a duplicate key last-wins, so a reader that took the
			// first would report on a block that is not in force.
			return mcpServersScan{}, errConfigDuplicate
		}
		if value != "" {
			return mcpServersScan{}, errConfigInline
		}
		start = i
	}
	if start < 0 {
		return mcpServersScan{}, nil
	}

	block, err := blockLines(lines[start+1:])
	if err != nil {
		return mcpServersScan{}, err
	}
	return scanServiceEntries(block)
}

// blockLines returns the indented lines belonging to a block, stopping at the
// first line back at column zero.
func blockLines(rest []string) ([]string, error) {
	var out []string
	for _, raw := range rest {
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		if strings.ContainsRune(leadingSpace(raw), '\t') {
			// YAML forbids tabs in indentation outright, so a document with them
			// is one Hermes and this reader would not agree about.
			return nil, errConfigTab
		}
		if indentOf(raw) == 0 {
			break
		}
		out = append(out, raw)
	}
	return out, nil
}

// scanServiceEntries walks the service mappings inside the block.
func scanServiceEntries(block []string) (mcpServersScan, error) {
	var scan mcpServersScan
	if len(block) == 0 {
		return scan, nil
	}
	serviceIndent := indentOf(block[0])

	command := ""
	closeService := func() error {
		if scan.Services == 0 {
			return nil
		}
		if command == "" {
			return errConfigNoCommand
		}
		if command != TorioMCPRelayPath {
			scan.Foreign++
		}
		return nil
	}

	for _, raw := range block {
		indent := indentOf(raw)
		if indent < serviceIndent {
			return mcpServersScan{}, errConfigShape
		}
		key, value, ok := splitYAMLEntry(raw)
		if !ok {
			// A list item, a bare scalar, a continuation — none of them is a
			// mapping entry, and none of them is a shape this reader models.
			return mcpServersScan{}, errConfigShape
		}
		if strings.HasPrefix(key, "<<") {
			return mcpServersScan{}, errConfigAnchor
		}
		if isAnchorOrAlias(value) {
			return mcpServersScan{}, errConfigAnchor
		}

		if indent == serviceIndent {
			if err := closeService(); err != nil {
				return mcpServersScan{}, err
			}
			if value != "" {
				return mcpServersScan{}, errConfigInline
			}
			scan.Services++
			command = ""
			continue
		}
		if key != "command" {
			continue
		}
		if command != "" {
			return mcpServersScan{}, errConfigTwoCmds
		}
		scalar, ok := scalarValue(value)
		if !ok {
			return mcpServersScan{}, errConfigInline
		}
		if scalar == "" {
			return mcpServersScan{}, errConfigNoCommand
		}
		command = scalar
	}
	if err := closeService(); err != nil {
		return mcpServersScan{}, err
	}
	return scan, nil
}

// splitYAMLEntry splits `key: value` and reports whether the line is a mapping
// entry at all. The value keeps its own quoting for the caller to judge.
func splitYAMLEntry(raw string) (key, value string, ok bool) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
		return "", "", false
	}
	i := strings.Index(line, ":")
	if i < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:i])
	value = strings.TrimSpace(line[i+1:])
	if key == "" {
		return "", "", false
	}
	// An unquoted value ends where a comment begins, and a comment begins only
	// after whitespace.
	if !strings.HasPrefix(value, `"`) && !strings.HasPrefix(value, "'") {
		if c := strings.Index(value, " #"); c >= 0 {
			value = strings.TrimSpace(value[:c])
		}
	}
	return key, value, true
}

// scalarValue unwraps a plain or quoted scalar, refusing every construct whose
// meaning depends on more than the line it is written on.
func scalarValue(value string) (string, bool) {
	switch {
	case value == "":
		return "", true
	case strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) && len(value) >= 2:
		return value[1 : len(value)-1], true
	case strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2:
		return value[1 : len(value)-1], true
	case strings.ContainsAny(value[:1], `{[|>"'`):
		return "", false
	}
	return value, true
}

// isAnchorOrAlias reports whether a value carries content from elsewhere in the
// document. Both are refused: what the entry means would then depend on a line
// this reader is not looking at.
func isAnchorOrAlias(value string) bool {
	return strings.HasPrefix(value, "&") || strings.HasPrefix(value, "*")
}

func leadingSpace(raw string) string {
	return raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
}

func indentOf(raw string) int {
	return len(leadingSpace(raw))
}
