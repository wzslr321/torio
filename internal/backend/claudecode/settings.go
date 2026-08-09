package claudecode

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
)

// The managed settings file.
//
// Read the next paragraph before adding anything to it, and before describing
// it to an operator.
//
// This file is a GUARDRAIL, not a boundary. It shapes how Claude Code behaves;
// it does not constrain what the process can do. The agent executes arbitrary
// code as its own uid, so it could ignore any setting here, and nothing in this
// package pretends otherwise. What root ownership buys is narrower and still
// worth having: the agent cannot silently retune its own guardrails, and a
// change to them is visible as drift. The boundaries are elsewhere and are
// enforced below the agent's privilege — the uid, the exact group set, the
// absent sudo, `ssh.forwardAgent: false`, and the edge of the VM.
//
// It is world-readable on purpose, for the same reason the MCP policy directory
// is: a constrained party that can read its own constraints, and cannot change
// them, is the whole transparency claim.
const (
	managedSettingsDir  = "/etc/claude-code"
	managedSettingsPath = managedSettingsDir + "/managed-settings.json"
)

//go:embed templates/managed-settings.json
var embeddedManagedSettings []byte

// ManagedSettings returns the exact bytes Torio installs. It is exported so a
// golden test can lock them: a change to what the box tells the agent about
// permissions or the updater should be a reviewed change to a file, not a
// side effect of editing a string.
func ManagedSettings() []byte { return embeddedManagedSettings }

// VerifyGuardrails proves the managed settings and the helper they run are the
// ones Torio wrote, and reports what MCP servers the guest is configured with.
//
// None of the three is a boundary and each says so where it is recorded. The
// first two are root-owned files whose content is compared by digest; the third
// reads a file the agent owns and can rewrite at will, so it is a drift detector
// in the strictest sense — it answers "what is configured right now", never
// "what is allowed".
//
// The helper is verified here rather than beside the session helper because it
// exists only for what the settings ask of it: the settings name it as a hook,
// so settings that were installed and a helper that was not is a guardrail with
// a hole in the middle, and both halves belong in one check.
func (claudeBackend) VerifyGuardrails(ctx context.Context, r backend.StepRunner) error {
	if err := reconcileManagedSettings(ctx, r); err != nil {
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

func reconcileManagedSettings(ctx context.Context, r backend.StepRunner) error {
	const name = "claude_managed_settings"

	kind, err := r.Probe(ctx, name, "stat", "-c", "%F", managedSettingsPath)
	if err != nil {
		return err
	}
	if kind.ExitCode == 1 && trimmed(kind.Stdout) == "" {
		absent, err := r.Probe(ctx, name, "test", "!", "-e", managedSettingsPath)
		if err != nil {
			return err
		}
		if absent.ExitCode != 0 {
			return r.Fail(name, "could not prove the managed settings path is absent",
				"inspect "+managedSettingsPath+" on the guest")
		}
		if !r.Reconcile() {
			return r.Fail(name, "no managed settings at "+managedSettingsPath,
				"run `torio vm bootstrap`, which installs them root-owned")
		}
		if err := installManagedSettings(ctx, r, name); err != nil {
			return err
		}
		r.Record(name+"_installed", true, "installed embedded settings atomically")
		kind, err = r.Probe(ctx, name, "stat", "-c", "%F", managedSettingsPath)
		if err != nil {
			return err
		}
	}
	if kind.ExitCode != 0 || trimmed(kind.Stdout) != "regular file" {
		return r.Fail(name, "managed settings is not a regular file",
			"a symlink here would move the settings somewhere the agent may own; remove it and re-run bootstrap")
	}

	og, err := r.Probe(ctx, name, "stat", "-c", "%U:%G %a", managedSettingsPath)
	if err != nil {
		return err
	}
	owner, group, mode, ok := parseOwnershipMode(og.Stdout)
	if og.ExitCode != 0 || !ok {
		return r.Fail(name, "could not read managed settings ownership/mode", "verify "+managedSettingsPath+" on the guest")
	}
	if owner != "root" || group != "root" {
		return r.Fail(name, fmt.Sprintf("managed settings owned by %s:%s, want root:root", owner, group),
			"settings the agent owns are settings the agent can retune between sessions")
	}
	if writable, parsed := modeGrantsForeignWrite(mode); !parsed || writable {
		return r.Fail(name, "managed settings mode "+mode+" is group- or world-writable",
			"reinstall it 0644 root:root")
	}

	// Content is compared by digest rather than by parsing: what matters is
	// that the file is the one Torio wrote, and a semantic comparison would
	// accept a document that means the same thing to a parser we do not own.
	sum, err := r.Probe(ctx, name, "sha256sum", "--", managedSettingsPath)
	if err != nil {
		return err
	}
	got := strings.Fields(string(sum.Stdout))
	want := sha256.Sum256(embeddedManagedSettings)
	wantHex := hex.EncodeToString(want[:])
	if sum.ExitCode != 0 || len(got) == 0 {
		return r.Fail(name, "could not read the managed settings digest", "verify "+managedSettingsPath+" on the guest")
	}
	if got[0] != wantHex {
		// Two different things reach here and the operator has to be able to
		// tell them apart: someone with root on the guest edited the file, or
		// Torio's own embedded settings moved on and this box still carries the
		// previous ones. Neither is repaired in place — a guardrail that rewrites
		// itself is one whose changes nobody reviews — so both are answered the
		// same way, by an operator who looks at the file and then removes it.
		return r.Fail(name, "managed settings content has drifted from the version Torio installs",
			"inspect "+managedSettingsPath+"; if it is the previous version Torio shipped, remove it and re-run `torio vm bootstrap` to install the current one")
	}
	r.Record(name, true, "root:root "+mode+" sha256="+wantHex[:12])
	return nil
}

// installManagedSettings writes the file atomically through a root-owned
// staging path in the same directory, so no reader ever sees a partial
// document and the final rename is a same-filesystem move.
func installManagedSettings(ctx context.Context, r backend.StepRunner, name string) error {
	script := `
tmp="$(mktemp ` + managedSettingsDir + `/.managed-settings.XXXXXX)"
trap 'rm -f -- "$tmp"' EXIT
cat >"$tmp"
chown root:root "$tmp"
chmod 0644 "$tmp"
sync -f "$tmp"
mv -T -- "$tmp" ` + managedSettingsPath + `
sync -f ` + managedSettingsDir + `
trap - EXIT
`
	res, err := r.ProbeInput(ctx, name, embeddedManagedSettings,
		[]string{"sudo", "-n", "/bin/bash", "-ceu", script})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return r.Fail(name, "could not install the managed settings file",
			"confirm passwordless root provisioning is intact and re-run bootstrap")
	}
	return nil
}

// mcpConfigPath is the agent's own configuration, which is where `claude mcp
// add` records the servers it has been given. The agent owns it.
const mcpConfigPath = Home + "/.claude.json"

// reportMCPServers enumerates the MCP servers the guest is configured with, by
// name.
//
// This reads Claude's agent-owned file as a drift detector only. The released
// MCP route lives in root-owned managed-mcp.json and managed settings exclude
// unmanaged servers (ADR-0013); a native entry here is nevertheless worth
// reporting because it can retain an old credential the agent uid can read.
// Names alone are emitted — never a value, token or endpoint. Bootstrap does
// not fail on this fact; `torio mcp status` is the command that verifies the
// custody boundary and rejects native entries.
func reportMCPServers(ctx context.Context, r backend.StepRunner) error {
	const name = mcpServersCheck
	res, err := r.Probe(ctx, name, "sudo", "-n", "-u", User, "--",
		"test", "-f", mcpConfigPath)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		r.Record(name, true, "none configured")
		return nil
	}
	// jq is not assumed on the guest, and the document can be large, so the
	// names are extracted by the smallest reliable filter available: the keys
	// of the mcpServers object, printed one per line and bounded by the
	// transport like every other probe.
	out, err := r.Probe(ctx, name, "sudo", "-n", "-u", User, "--",
		"python3", "-c", mcpNamesProgram, mcpConfigPath)
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
// nothing else. It reads only keys: a value in this document may be a token,
// and a program that could print one would eventually be asked to.
const mcpNamesProgram = `
import json,sys,re
try:
    d=json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
names=set()
def collect(o):
    s=o.get("mcpServers")
    if isinstance(s,dict): names.update(s.keys())
collect(d)
for p in (d.get("projects") or {}).values():
    if isinstance(p,dict): collect(p)
for n in sorted(names):
    if re.fullmatch(r"[A-Za-z0-9_.-]{1,64}",n): print(n)
`
