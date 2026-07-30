package lima

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	TorioMCPBrokerArtifact = "torio-mcp-broker-linux-arm64"
	TorioMCPRelayArtifact  = "torio-mcp-connect-linux-arm64"
	maxMCPGuestBinarySize  = 128 << 20
)

type mcpGuestBinary struct {
	artifact string
	target   string
	body     []byte
	digest   string
}

func (a *Adapter) loadMCPGuestBinaries() ([]mcpGuestBinary, error) {
	dir := a.MCPGuestBinaryDir
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("locate packaged MCP guest binaries")}
		}
		dir = filepath.Dir(exe)
	}

	specs := []struct {
		artifact string
		target   string
	}{
		{TorioMCPBrokerArtifact, TorioMCPBrokerPath},
		{TorioMCPRelayArtifact, TorioMCPRelayPath},
	}
	binaries := make([]mcpGuestBinary, 0, len(specs))
	for _, spec := range specs {
		path := filepath.Join(dir, spec.artifact)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxMCPGuestBinarySize {
			return nil, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
				Err: fmt.Errorf("packaged MCP guest binary %s is missing or invalid", spec.artifact)}
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
				Err: fmt.Errorf("read packaged MCP guest binary %s", spec.artifact)}
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(body))
		binaries = append(binaries, mcpGuestBinary{artifact: spec.artifact, target: spec.target, body: body, digest: digest})
	}
	return binaries, nil
}

func (a *Adapter) ensureMCPGuestBinary(ctx context.Context, rep *MCPBrokerInstallReport, bin mcpGuestBinary) (bool, error) {
	name := "install:binary:" + filepath.Base(bin.target)
	present, exact, err := a.probeMCPGuestBinary(ctx, rep, name, bin)
	if err != nil {
		return false, err
	}
	if present && exact {
		rep.record(name, true, "root:root 755")
		return false, nil
	}

	staging := bin.target + ".new"
	if err := a.installMutate(ctx, rep, name, "clear stale staging path", "sudo", "-n", "rm", "-f", "--", staging); err != nil {
		return false, err
	}
	res, err := a.SSHInput(ctx, bin.body,
		[]string{"sudo", "-n", "dd", "of=" + staging, "status=none", "conv=fsync", "oflag=excl,nofollow"})
	if err != nil {
		return false, err
	}
	if res.ExitCode != 0 {
		rep.record(name, false, "staging write failed")
		return false, &Error{Op: mcpInstallOp, Kind: KindCommandFailed,
			Err: fmt.Errorf("%s: staging write exited %d", name, res.ExitCode)}
	}
	if err := a.installMutate(ctx, rep, name, "chmod", "sudo", "-n", "chmod", "0755", staging); err != nil {
		return false, err
	}
	if err := a.installMutate(ctx, rep, name, "atomic install", "sudo", "-n", "mv", "-f", staging, bin.target); err != nil {
		return false, err
	}
	if err := a.installMutate(ctx, rep, name, "sync install directory", "sudo", "-n", "sync", "-f", "/usr/local/bin"); err != nil {
		return false, err
	}

	present, exact, err = a.probeMCPGuestBinary(ctx, rep, name, bin)
	if err != nil {
		return false, err
	}
	if !present || !exact {
		rep.record(name, false, "installed binary failed verification")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: installed binary failed digest or ownership verification", name)}
	}
	rep.record(name, true, "installed root:root 755")
	return true, nil
}

// probeMCPGuestBinary distinguishes an absent target from a privileged probe
// that never ran by statting /usr/local/bin in the same invocation. exact means
// both root:root 0755 and a digest equal to the packaged payload.
func (a *Adapter) probeMCPGuestBinary(ctx context.Context, rep *MCPBrokerInstallReport, name string, bin mcpGuestBinary) (present, exact bool, retErr error) {
	res, err := a.installProbe(ctx, "sudo", "-n", "stat", "-c", "%F %U:%G %a", "/usr/local/bin", bin.target)
	if err != nil {
		return false, false, err
	}
	lines := nonEmptyLines(res.out)
	if len(lines) == 0 || lines[0] != "directory root:root 755" {
		rep.record(name, false, "binary parent is not a trusted root directory")
		return false, false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: binary parent must be root:root 0755", name)}
	}
	if len(lines) == 1 {
		return false, false, nil
	}
	if len(lines) != 2 {
		rep.record(name, false, "unparseable guest binary stat")
		return false, false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: unparseable guest binary stat", name)}
	}
	metadataExact := lines[1] == "regular file root:root 755"

	digest, err := a.installProbe(ctx, "sudo", "-n", "sha256sum", bin.target)
	if err != nil {
		return true, false, err
	}
	fields := strings.Fields(digest.out)
	if digest.exit != 0 || len(fields) != 2 || fields[1] != bin.target {
		rep.record(name, false, "could not verify guest binary digest")
		return true, false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: could not verify guest binary digest", name)}
	}
	return true, metadataExact && fields[0] == bin.digest, nil
}

func nonEmptyLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
