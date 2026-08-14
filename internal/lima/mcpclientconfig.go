package lima

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
)

// ClaudeManagedMCPPath is Claude Code's enterprise-managed MCP configuration.
// It is root-owned and paired with allowManagedMcpServersOnly in Torio's
// managed settings, so user/project/plugin MCP entries cannot bypass it.
const ClaudeManagedMCPPath = "/etc/claude-code/managed-mcp.json"

type mcpClientConfigInput struct {
	Services []string `json:"services"`
	// Rendered carries a document the host produced whole. It exists for a
	// backend whose file the host must be able to compare byte for byte, which
	// means the bytes cannot be assembled on the guest by a program that might
	// format them differently than the verifier expects.
	Rendered string `json:"rendered,omitempty"`
}

func (a *Adapter) reconcileMCPClientConfig(ctx context.Context, identity backend.Identity, grant PolicyGrant, rep *MCPBrokerInstallReport) (bool, error) {
	// Codex takes a different route through this step and says so here rather
	// than inside the shared program: its root-owned file is an allowlist the
	// host renders whole, and its declarations are written by the agent's own
	// command instead of by editing a file Torio does not own the format of.
	if identity.Name == "codex" {
		return a.reconcileCodexMCPConfig(ctx, identity, grant, rep)
	}

	input := mcpClientConfigInput{Services: make([]string, 0, len(grant.Services))}
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

	var kind, path, python string
	var argv []string
	switch identity.Name {
	case "hermes":
		kind, path, python = "hermes", HermesConfigPath, hermesAgentDir+"/venv/bin/python"
		argv = []string{"sudo", "-n", "-u", identity.GuestUser, "--", python, "-c", reconcileMCPClientProgram, kind, path, TorioMCPRelayPath}
	case "claude-code":
		kind, path, python = "claude", ClaudeManagedMCPPath, "/usr/bin/python3"
		argv = []string{"sudo", "-n", "--", python, "-c", reconcileMCPClientProgram, kind, path, TorioMCPRelayPath}
	default:
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("backend %q has no verified MCP client configuration contract", identity.Name)}
	}

	res, err := a.SSHInput(ctx, body, argv)
	if err != nil {
		return false, err
	}
	if res.StdoutTruncated || res.StderrTruncated || res.ExitCode != 0 {
		rep.record("install:agent_mcp_config", false, "configuration reconcile failed")
		return false, &Error{Op: mcpInstallOp, Kind: KindCommandFailed, Err: fmt.Errorf("agent MCP configuration reconcile exited %d", res.ExitCode)}
	}
	changed, ok := mcpConfigChanged(res.Stdout)
	if !ok {
		rep.record("install:agent_mcp_config", false, "configuration result was not verifiable")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("agent MCP configuration returned an unknown result")}
	}
	if identity.Name == "claude-code" {
		scrub, err := a.SSHInput(ctx, nil, []string{"sudo", "-n", "-u", identity.GuestUser, "--", python, "-c", scrubClaudeNativeMCPProgram, identity.Home + "/.claude.json"})
		if err != nil {
			return changed, err
		}
		if scrub.StdoutTruncated || scrub.StderrTruncated || scrub.ExitCode != 0 {
			rep.record("install:agent_mcp_config", false, "native MCP declaration removal failed")
			return changed, &Error{Op: mcpInstallOp, Kind: KindCommandFailed, Err: fmt.Errorf("native MCP declaration removal exited %d", scrub.ExitCode)}
		}
		scrubChanged, ok := mcpConfigChanged(scrub.Stdout)
		if !ok {
			return changed, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("native MCP declaration removal returned an unknown result")}
		}
		changed = changed || scrubChanged
	}
	if changed {
		rep.record("install:agent_mcp_config", true, identity.Name+" configured through relay")
		return true, nil
	}
	rep.record("install:agent_mcp_config", true, identity.Name+" already configured through relay")
	return false, nil
}

func mcpConfigChanged(output []byte) (changed, ok bool) {
	switch strings.TrimSpace(string(output)) {
	case "changed":
		return true, true
	case "unchanged":
		return false, true
	default:
		return false, false
	}
}

// This program changes only the backend's MCP declaration. For Hermes the file
// is agent-writable, so this is a drift detector and convenience, never the
// authorization boundary. Claude's separate managed MCP file is root-owned and
// its pinned managed settings reject every unmanaged server. Both clients still
// meet the same kernel boundary at the broker socket.
const reconcileMCPClientProgram = `
import json,os,re,stat,sys,tempfile
kind,path,relay=sys.argv[1:4]
payload=json.load(sys.stdin)
services=payload.get("services")
if not isinstance(services,list) or any(not isinstance(s,str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,63}",s) for s in services):
    raise SystemExit(2)
if len(set(services)) != len(services):
    raise SystemExit(2)
if os.path.lexists(path):
    st=os.lstat(path)
    if not stat.S_ISREG(st.st_mode):
        raise SystemExit(3)
def commit(target,rendered,mode):
    try:
        with open(target,"rb") as f:
            if f.read() == rendered:
                return False
    except FileNotFoundError:
        pass
    parent=os.path.dirname(target)
    fd,tmp=tempfile.mkstemp(prefix=".torio-mcp-config.",dir=parent)
    try:
        with os.fdopen(fd,"wb") as f:
            f.write(rendered)
            f.flush()
            os.fchmod(f.fileno(),mode)
            os.fsync(f.fileno())
        os.replace(tmp,target)
        dfd=os.open(parent,os.O_RDONLY|os.O_DIRECTORY)
        try:
            os.fsync(dfd)
        finally:
            os.close(dfd)
    finally:
        try: os.unlink(tmp)
        except FileNotFoundError: pass
    return True
changed=False
if kind == "hermes":
    import yaml
    if os.path.exists(path):
        with open(path,"r",encoding="utf-8") as f:
            document=yaml.safe_load(f) or {}
    else:
        document={}
    if not isinstance(document,dict):
        raise SystemExit(4)
    document["mcp_servers"]={s:{"command":relay,"args":[s],"enabled":True} for s in services}
    rendered=yaml.safe_dump(document,sort_keys=False,allow_unicode=True).encode()
    changed=commit(path,rendered,0o600)
elif kind == "claude":
    document={"mcpServers":{s:{"type":"stdio","command":relay,"args":[s],"env":{}} for s in services}}
    rendered=(json.dumps(document,indent=2,sort_keys=True)+"\n").encode()
    changed=commit(path,rendered,0o644)
elif kind == "codex":
    document=payload.get("rendered")
    if not isinstance(document,str) or not document:
        raise SystemExit(6)
    changed=commit(path,document.encode(),0o644)
else:
    raise SystemExit(5)
print("changed" if changed else "unchanged")
`

// Claude owns this file, so removal runs as Claude rather than letting root
// create files in an agent-writable directory. It is migration/drift cleanup,
// not an enforcement boundary; root-owned managed configuration excludes these
// entries even if the agent adds them again later.
const scrubClaudeNativeMCPProgram = `
import json,os,stat,sys,tempfile
path=sys.argv[1]
if not os.path.lexists(path):
    print("unchanged")
    raise SystemExit(0)
st=os.lstat(path)
if not stat.S_ISREG(st.st_mode):
    raise SystemExit(2)
with open(path,"r",encoding="utf-8") as f:
    document=json.load(f)
if not isinstance(document,dict):
    raise SystemExit(3)
changed=document.pop("mcpServers",None) is not None
projects=document.get("projects")
if isinstance(projects,dict):
    for project in projects.values():
        if isinstance(project,dict):
            changed=(project.pop("mcpServers",None) is not None) or changed
if not changed:
    print("unchanged")
    raise SystemExit(0)
rendered=(json.dumps(document,indent=2,sort_keys=True)+"\n").encode()
parent=os.path.dirname(path)
fd,tmp=tempfile.mkstemp(prefix=".torio-mcp-native.",dir=parent)
try:
    with os.fdopen(fd,"wb") as f:
        f.write(rendered)
        f.flush()
        os.fchmod(f.fileno(),0o600)
        os.fsync(f.fileno())
    os.replace(tmp,path)
    dfd=os.open(parent,os.O_RDONLY|os.O_DIRECTORY)
    try: os.fsync(dfd)
    finally: os.close(dfd)
finally:
    try: os.unlink(tmp)
    except FileNotFoundError: pass
print("changed")
`
