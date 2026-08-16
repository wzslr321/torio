package lima

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TorioMCPRelayPath is the only command an MCP server declaration may name on a
// managed guest (ADR-0004 §3). The relay itself holds no secret and is not a
// control — the agent may bypass it and talk to the socket directly, and nothing
// changes. What matters is that every configured server *ends up* at the broker.
const TorioMCPRelayPath = "/usr/local/bin/torio-mcp-connect"

const maxMCPPayloadBytes = 128 << 20

type mcpInstallFile struct {
	name string
	dst  string
	mode string
	body []byte
	sum  string
}

// installMCPPayloadFiles installs the two architecture-specific guest
// executables shipped beside torio and the embedded systemd unit. Every final
// path is replaced only after its complete bytes have been fsynced in the same
// directory. No shell command string is involved; every mutation is a fixed
// argv and file content travels only on stdin.
func (a *Adapter) installMCPPayloadFiles(ctx context.Context, payloadDir string, rep *MCPBrokerInstallReport) (bool, error) {
	p, err := a.profile()
	if err != nil {
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: err}
	}
	broker, err := readReleasePayload(payloadDir, p.MCPBrokerArtifact())
	if err != nil {
		return false, &Error{Op: mcpInstallOp, Kind: KindNotFound, Err: err}
	}
	relay, err := readReleasePayload(payloadDir, p.MCPRelayArtifact())
	if err != nil {
		return false, &Error{Op: mcpInstallOp, Kind: KindNotFound, Err: err}
	}
	files := []mcpInstallFile{
		newMCPInstallFile("broker_payload", TorioMCPBrokerPath, "0755", broker),
		newMCPInstallFile("relay_payload", TorioMCPRelayPath, "0755", relay),
		newMCPInstallFile("broker_unit", TorioMCPBrokerUnitPath, "0644", mcpBrokerUnit()),
	}
	changed := false
	for _, f := range files {
		oneChanged, err := a.ensureMCPInstallFile(ctx, rep, f)
		changed = changed || oneChanged
		if err != nil {
			return changed, err
		}
	}
	if changed {
		if err := a.installMutate(ctx, rep, "install:systemd_reload", "systemctl daemon-reload",
			"sudo", "-n", "systemctl", "daemon-reload"); err != nil {
			return true, err
		}
		rep.record("install:systemd_reload", true, "reloaded")
	}
	return changed, nil
}

func newMCPInstallFile(name, dst, mode string, body []byte) mcpInstallFile {
	sum := sha256.Sum256(body)
	return mcpInstallFile{name: name, dst: dst, mode: mode, body: body, sum: hex.EncodeToString(sum[:])}
}

func readReleasePayload(dir, name string) ([]byte, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("MCP payload directory is not absolute")
	}
	path := filepath.Join(dir, name)
	st, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("MCP release payload %s is unavailable", name)
	}
	if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("MCP release payload %s is not a regular executable", name)
	}
	if st.Size() <= 0 || st.Size() > maxMCPPayloadBytes {
		return nil, fmt.Errorf("MCP release payload %s has an invalid size", name)
	}
	body, err := os.ReadFile(path)
	if err != nil || int64(len(body)) != st.Size() {
		return nil, fmt.Errorf("MCP release payload %s could not be read completely", name)
	}
	return body, nil
}

func (a *Adapter) ensureMCPInstallFile(ctx context.Context, rep *MCPBrokerInstallReport, f mcpInstallFile) (bool, error) {
	name := "install:" + f.name
	metadata, err := a.installProbe(ctx, "sudo", "-n", "stat", "-c", "%U:%G %a %F", f.dst)
	if err != nil {
		return false, err
	}
	if metadata.exit == 0 && metadata.trimmed() == "root:root "+strings.TrimPrefix(f.mode, "0")+" regular file" {
		digest, err := a.installProbe(ctx, "sudo", "-n", "sha256sum", f.dst)
		if err != nil {
			return false, err
		}
		if digest.exit == 0 && firstField(digest.out) == f.sum {
			rep.record(name, true, "present sha256="+f.sum[:12])
			return false, nil
		}
	}

	tmp := f.dst + ".torio-new"
	write, err := a.SSHInput(ctx, f.body, []string{"sudo", "-n", "dd", "of=" + tmp, "status=none", "conv=fsync"})
	if err != nil {
		return false, err
	}
	if write.ExitCode != 0 {
		rep.record(name, false, "atomic write failed")
		return false, &Error{Op: mcpInstallOp, Kind: KindCommandFailed, Err: fmt.Errorf("%s: payload write exited %d", name, write.ExitCode)}
	}
	mutations := []struct {
		detail string
		argv   []string
	}{
		{"chown", []string{"sudo", "-n", "chown", "root:root", tmp}},
		{"chmod", []string{"sudo", "-n", "chmod", f.mode, tmp}},
		{"rename", []string{"sudo", "-n", "mv", "-T", "--", tmp, f.dst}},
		{"directory fsync", []string{"sudo", "-n", "sync", "-f", filepath.Dir(f.dst)}},
	}
	for _, mutation := range mutations {
		if err := a.installMutate(ctx, rep, name, mutation.detail, mutation.argv...); err != nil {
			return true, err
		}
	}

	verified, err := a.installProbe(ctx, "sudo", "-n", "stat", "-c", "%U:%G %a %F", f.dst)
	if err != nil {
		return true, err
	}
	digest, err := a.installProbe(ctx, "sudo", "-n", "sha256sum", f.dst)
	if err != nil {
		return true, err
	}
	if verified.exit != 0 || verified.trimmed() != "root:root "+strings.TrimPrefix(f.mode, "0")+" regular file" ||
		digest.exit != 0 || firstField(digest.out) != f.sum {
		rep.record(name, false, "installed bytes could not be verified")
		return true, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("%s: installed bytes did not verify", name)}
	}
	rep.record(name, true, "installed sha256="+f.sum[:12])
	return true, nil
}

func firstField(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
