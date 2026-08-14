package codex

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
)

// The system configuration.
//
// Read the template's own header before adding anything to this file, and before
// describing it to an operator: it is a guardrail and not a boundary, and every
// name in this package is chosen to keep those apart.
//
// Codex reads a system layer on Linux that the agent's own configuration cannot
// displace. That is what makes a guardrail possible here at all: the file Torio
// writes is one the agent cannot write.
//
// There were two root-owned paths this could have been, and the choice matters.
// `/etc/codex/config.toml` is the system configuration layer. The other,
// `/etc/codex/managed_config.toml`, is named legacy where it is read, and it is
// loaded twice: once as configuration and once reinterpreted as an admin
// requirement. Settings placed there would constrain what may later be chosen as
// well as choosing it, which is a second effect nobody asked for and one this
// package could not honestly describe. The requirement Torio does want, the MCP
// allowlist, goes to `/etc/codex/requirements.toml`, where it is the only thing
// that file says.
const systemConfigPath = systemConfigDir + "/config.toml"

//go:embed templates/config.toml
var embeddedSystemConfig []byte

// VerifyGuardrails proves the system configuration and the helper it names are
// the ones Torio wrote, and reports what MCP servers the agent has configured.
//
// None of the four is a boundary and each says so where it is recorded. The
// first three compare root-owned content by digest or prove agent-owned
// ownership; the last reads a file the agent owns and rewrites at will, so it is
// a drift detector in the strictest sense: it answers what is configured right
// now, never what is allowed.
//
// The helper is verified here rather than beside the session helper because it
// exists only for what the configuration asks of it. A configuration that was
// installed and a helper that was not is a guardrail with a hole in the middle,
// and both halves belong in one check.
func (codexBackend) VerifyGuardrails(ctx context.Context, r backend.StepRunner) error {
	if err := reconcileSystemConfig(ctx, r); err != nil {
		return err
	}
	if err := reconcileWaitingMarkerHelper(ctx, r); err != nil {
		return err
	}
	if err := reconcileWaitingMarkerState(ctx, r); err != nil {
		return err
	}
	return reportMCPServers(ctx, r)
}

func reconcileSystemConfig(ctx context.Context, r backend.StepRunner) error {
	const name = "codex_system_config"

	kind, err := r.Probe(ctx, name, "stat", "-c", "%F", systemConfigPath)
	if err != nil {
		return err
	}
	if kind.ExitCode == 1 && trimmed(kind.Stdout) == "" {
		absent, err := r.Probe(ctx, name, "test", "!", "-e", systemConfigPath)
		if err != nil {
			return err
		}
		if absent.ExitCode != 0 {
			return r.Fail(name, "could not prove the system configuration path is absent",
				"inspect "+systemConfigPath+" on the guest")
		}
		if !r.Reconcile() {
			return r.Fail(name, "no system configuration at "+systemConfigPath,
				"run `torio vm bootstrap`, which installs it root-owned")
		}
		res, err := r.ProbeInput(ctx, name, embeddedSystemConfig, systemConfigInstallArgv())
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return r.Fail(name, "could not install the system configuration",
				"confirm passwordless root provisioning is intact and re-run bootstrap")
		}
		r.Record(name+"_installed", true, "installed embedded configuration atomically")
		kind, err = r.Probe(ctx, name, "stat", "-c", "%F", systemConfigPath)
		if err != nil {
			return err
		}
	}
	if kind.ExitCode != 0 || trimmed(kind.Stdout) != "regular file" {
		return r.Fail(name, "the system configuration is not a regular file",
			"a symlink here would move the guardrails somewhere the agent may own; remove it and re-run bootstrap")
	}

	og, err := r.Probe(ctx, name, "stat", "-c", "%U:%G %a", systemConfigPath)
	if err != nil {
		return err
	}
	owner, group, mode, ok := parseOwnershipMode(og.Stdout)
	if og.ExitCode != 0 || !ok {
		return r.Fail(name, "could not read the system configuration ownership/mode",
			"verify "+systemConfigPath+" on the guest")
	}
	if owner != "root" || group != "root" {
		return r.Fail(name, fmt.Sprintf("system configuration owned by %s:%s, want root:root", owner, group),
			"a configuration the agent owns is one the agent can retune between sessions")
	}
	if writable, parsed := modeGrantsForeignWrite(mode); !parsed || writable {
		return r.Fail(name, "system configuration mode "+mode+" is group- or world-writable",
			"reinstall it 0644 root:root")
	}

	// Content is compared by digest rather than by parsing. What matters is that
	// the file is the one Torio wrote, and a semantic comparison would accept a
	// document that means the same thing to a parser we do not own.
	sum, err := r.Probe(ctx, name, "sha256sum", "--", systemConfigPath)
	if err != nil {
		return err
	}
	got := strings.Fields(string(sum.Stdout))
	want := sha256.Sum256(embeddedSystemConfig)
	wantHex := hex.EncodeToString(want[:])
	if sum.ExitCode != 0 || len(got) == 0 {
		return r.Fail(name, "could not read the system configuration digest",
			"verify "+systemConfigPath+" on the guest")
	}
	if got[0] != wantHex {
		// Two different things reach here and the operator has to tell them
		// apart: somebody with root on the guest edited the file, or Torio's own
		// embedded configuration moved on and this box still carries the previous
		// one. Neither is repaired in place, because a guardrail that rewrites
		// itself is one whose changes nobody reviews.
		return r.Fail(name, "system configuration has drifted from the version Torio installs",
			"inspect "+systemConfigPath+"; if it is the previous version Torio shipped, remove it and re-run `torio vm bootstrap` to install the current one")
	}
	r.Record(name, true, "root:root "+mode+" sha256="+wantHex[:12])
	return nil
}

// systemConfigInstallArgv writes the file atomically through a root-owned
// staging path in the same directory, so no reader ever sees a partial document
// and the final rename is a same-filesystem move.
func systemConfigInstallArgv() []string {
	script := `
tmp="$(mktemp ` + systemConfigDir + `/.config.XXXXXX)"
trap 'rm -f -- "$tmp"' EXIT
cat >"$tmp"
chown root:root "$tmp"
chmod 0644 "$tmp"
sync -f "$tmp"
mv -T -- "$tmp" ` + systemConfigPath + `
sync -f ` + systemConfigDir + `
trap - EXIT
`
	return []string{"sudo", "-n", "/bin/bash", "-ceu", script}
}

// agentConfigPath is the agent's own configuration, which is where `codex mcp
// add` records the servers it has been given. The agent owns it.
const agentConfigPath = ProfilePath + "/config.toml"

// reportMCPServers enumerates the MCP servers the agent has configured, by name.
//
// This reads an agent-owned file as a drift detector only. The authorization
// lives in the root-owned requirements layer, which decides which of these
// declarations may run; an entry here is nevertheless worth reporting because it
// can retain an old credential the agent uid can read. Names alone are emitted,
// never a value, token or endpoint. Bootstrap does not fail on this fact;
// `torio mcp status` is the command that verifies the custody boundary.
func reportMCPServers(ctx context.Context, r backend.StepRunner) error {
	const name = mcpServersCheck
	res, err := r.Probe(ctx, name, "sudo", "-n", "-u", User, "--", "test", "-f", agentConfigPath)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		r.Record(name, true, "none configured")
		return nil
	}
	out, err := r.Probe(ctx, name, "sudo", "-n", "-u", User, "--",
		"python3", "-c", mcpNamesProgram, agentConfigPath)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		r.Record(name, true, "unreadable (agent-owned configuration)")
		return nil
	}
	names := strings.Fields(string(out.Stdout))
	if len(names) == 0 {
		r.Record(name, true, "none configured")
		return nil
	}
	r.Record(name, true, fmt.Sprintf("%d configured (agent-owned, not verified): %s", len(names), strings.Join(names, " ")))
	return nil
}

// mcpNamesProgram prints the configured MCP server names, one per line, and
// nothing else. It reads only table names: a value in this document may be a
// token, and a program that could print one would eventually be asked to.
//
// tomllib is in the standard library of the Python the guest image ships, so
// this adds no dependency the guest did not already have.
const mcpNamesProgram = `
import sys,re,tomllib
try:
    d=tomllib.load(open(sys.argv[1],"rb"))
except Exception:
    sys.exit(1)
s=d.get("mcp_servers")
if not isinstance(s,dict): sys.exit(0)
for n in sorted(s):
    if re.fullmatch(r"[A-Za-z0-9_.-]{1,64}",n): print(n)
`
