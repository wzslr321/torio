package lima

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

func (a *Adapter) ensureMCPBrokerUnit(ctx context.Context, rep *MCPBrokerInstallReport, expectedBrokerDigest, expectedPolicyDigest string) (bool, error) {
	const name = "install:unit"
	unit := mcpBrokerUnit()
	present, exact, err := a.probeMCPBrokerUnit(ctx, rep, name, unit)
	if err != nil {
		return false, err
	}
	unitChanged := !present || !exact
	verifyPath := TorioMCPBrokerUnitPath
	if unitChanged {
		if err := a.installMutate(ctx, rep, name, "clear stale staging path", "sudo", "-n", "rm", "-f", "--", mcpBrokerStagingPath); err != nil {
			return false, err
		}
		res, err := a.SSHInput(ctx, unit,
			[]string{"sudo", "-n", "dd", "of=" + mcpBrokerStagingPath, "status=none", "conv=fsync", "oflag=excl,nofollow"})
		if err != nil {
			return false, err
		}
		if res.ExitCode != 0 {
			rep.record(name, false, "staging write failed")
			return false, &Error{Op: mcpInstallOp, Kind: KindCommandFailed,
				Err: fmt.Errorf("%s: staging write exited %d", name, res.ExitCode)}
		}
		if err := a.installMutate(ctx, rep, name, "chmod", "sudo", "-n", "chmod", "0644", mcpBrokerStagingPath); err != nil {
			return false, err
		}
		verifyPath = mcpBrokerStagingPath
	}

	verified, err := a.installProbe(ctx, "sudo", "-n", "systemd-analyze", "verify", verifyPath)
	if err != nil {
		return false, err
	}
	if verified.exit != 0 {
		rep.record(name, false, "systemd unit validation failed")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: systemd-analyze verify exited %d", name, verified.exit)}
	}

	if unitChanged {
		if err := a.installMutate(ctx, rep, name, "atomic install", "sudo", "-n", "mv", "-f", mcpBrokerStagingPath, TorioMCPBrokerUnitPath); err != nil {
			return false, err
		}
		if err := a.installMutate(ctx, rep, name, "sync system unit directory", "sudo", "-n", "sync", "-f", "/etc/systemd/system"); err != nil {
			return false, err
		}
		if err := a.verifyMCPBrokerUnitMetadata(ctx, rep, name); err != nil {
			return false, err
		}
		installed, err := a.installProbe(ctx, "sudo", "-n", "systemd-analyze", "verify", TorioMCPBrokerUnitPath)
		if err != nil {
			return false, err
		}
		if installed.exit != 0 {
			rep.record(name, false, "installed systemd unit validation failed")
			return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
				Err: fmt.Errorf("%s: installed systemd unit validation exited %d", name, installed.exit)}
		}
	}
	daemonReloadRequired := unitChanged
	if !unitChanged {
		daemonReloadRequired, err = a.mcpBrokerNeedDaemonReload(ctx, rep, name)
		if err != nil {
			return false, err
		}
	}
	if daemonReloadRequired {
		if err := a.installMutate(ctx, rep, name, "daemon reload", "sudo", "-n", "systemctl", "daemon-reload"); err != nil {
			return false, err
		}
	}
	if err := a.verifyEffectiveMCPBrokerUnit(ctx, rep, name); err != nil {
		return false, err
	}

	enabled, err := a.mcpBrokerUnitEnabled(ctx, rep, name)
	if err != nil {
		return false, err
	}
	active, err := a.mcpBrokerUnitActive(ctx, rep, name)
	if err != nil {
		return false, err
	}
	activationChanged := !enabled || !active
	runningExact := false
	runningPolicyExact := false
	if active {
		runningExact, err = a.mcpBrokerRunningDigestExact(ctx, rep, name, expectedBrokerDigest)
		if err != nil {
			return false, err
		}
		runningPolicyExact, err = a.mcpBrokerRunningPolicyExact(ctx, rep, name, expectedPolicyDigest)
		if err != nil {
			return false, err
		}
	}
	restartRequired := active && (daemonReloadRequired || !runningExact || !runningPolicyExact)
	if activationChanged {
		if err := a.installMutate(ctx, rep, name, "activate", "sudo", "-n", "systemctl", "enable", "--now", TorioMCPBrokerUnitName); err != nil {
			return false, err
		}
	}
	if restartRequired {
		if err := a.installMutate(ctx, rep, name, "restart", "sudo", "-n", "systemctl", "restart", TorioMCPBrokerUnitName); err != nil {
			return false, err
		}
	}
	if activationChanged || restartRequired {
		if enabled, err = a.mcpBrokerUnitEnabled(ctx, rep, name); err != nil || !enabled {
			if err != nil {
				return false, err
			}
			return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
				Err: fmt.Errorf("%s: broker unit is not enabled after activation", name)}
		}
		if active, err = a.mcpBrokerUnitActive(ctx, rep, name); err != nil || !active {
			if err != nil {
				return false, err
			}
			return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
				Err: fmt.Errorf("%s: broker unit is not active after activation", name)}
		}
	}
	if activationChanged || restartRequired {
		runningExact, err = a.mcpBrokerRunningDigestExact(ctx, rep, name, expectedBrokerDigest)
		if err != nil {
			return false, err
		}
		runningPolicyExact, err = a.mcpBrokerRunningPolicyExact(ctx, rep, name, expectedPolicyDigest)
		if err != nil {
			return false, err
		}
	}
	if !runningExact {
		rep.record(name, false, "running broker executable digest does not match the packaged binary")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: running broker generation is stale after activation", name)}
	}
	if !runningPolicyExact {
		rep.record(name, false, "running broker policy generation does not match the verified policy documents")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: running broker policy generation is stale after activation", name)}
	}
	if err := a.verifyMCPRuntimeDirectory(ctx, rep); err != nil {
		return false, err
	}
	rep.record(name, true, "validated, enabled, active")
	return unitChanged || daemonReloadRequired || activationChanged || restartRequired, nil
}

func (a *Adapter) verifyEffectiveMCPBrokerUnit(ctx context.Context, rep *MCPBrokerInstallReport, name string) error {
	res, err := a.installProbe(ctx, mcpBrokerEffectiveUnitShowArgs()...)
	if err != nil {
		return err
	}
	if res.exit != 0 || !effectiveMCPBrokerUnitExact(res.out) {
		rep.record(name, false, "effective broker system unit has drop-ins, runtime overrides, or stale manager state")
		return &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: effective broker system unit drift", name)}
	}
	return nil
}

func (a *Adapter) mcpBrokerRunningPolicyExact(ctx context.Context, rep *MCPBrokerInstallReport, name, expected string) (bool, error) {
	if len(expected) != 64 {
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: verified policy digest is invalid", name)}
	}
	res, err := a.installProbe(ctx, "sudo", "-n", "cat", torioMCPPolicyDigestPath)
	if err != nil {
		return false, err
	}
	return res.exit == 0 && res.trimmed() == expected, nil
}

func (a *Adapter) mcpBrokerNeedDaemonReload(ctx context.Context, rep *MCPBrokerInstallReport, name string) (bool, error) {
	res, err := a.installProbe(ctx, "sudo", "-n", "systemctl", "show", "--property=NeedDaemonReload", "--value", TorioMCPBrokerUnitName)
	if err != nil {
		return false, err
	}
	switch res.trimmed() {
	case "no":
		if res.exit == 0 {
			return false, nil
		}
	case "yes":
		if res.exit == 0 {
			return true, nil
		}
	}
	rep.record(name, false, "could not establish whether systemd has stale unit state")
	return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
		Err: fmt.Errorf("%s: unexpected NeedDaemonReload result %q (exit %d)", name, res.trimmed(), res.exit)}
}

func (a *Adapter) mcpBrokerRunningDigestExact(ctx context.Context, rep *MCPBrokerInstallReport, name, expected string) (bool, error) {
	if len(expected) != 64 {
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: packaged broker digest is invalid", name)}
	}
	pidResult, err := a.installProbe(ctx, "sudo", "-n", "systemctl", "show", "--property=MainPID", "--value", TorioMCPBrokerUnitName)
	if err != nil {
		return false, err
	}
	pid, parseErr := strconv.Atoi(pidResult.trimmed())
	if pidResult.exit != 0 || parseErr != nil || pid <= 1 {
		rep.record(name, false, "active broker has no valid MainPID")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: could not establish the running broker process", name)}
	}
	procExe := fmt.Sprintf("/proc/%d/exe", pid)
	digestResult, err := a.installProbe(ctx, "sudo", "-n", "sha256sum", procExe)
	if err != nil {
		return false, err
	}
	fields := strings.Fields(digestResult.out)
	if digestResult.exit != 0 || len(fields) != 2 || len(fields[0]) != 64 {
		rep.record(name, false, "could not hash the running broker executable")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: running broker digest is unprovable", name)}
	}
	return fields[0] == expected, nil
}

func (a *Adapter) verifyMCPBrokerUnitMetadata(ctx context.Context, rep *MCPBrokerInstallReport, name string) error {
	res, err := a.installProbe(ctx, "sudo", "-n", "stat", "-c", "%F %U:%G %a", "/etc/systemd/system", TorioMCPBrokerUnitPath)
	if err != nil {
		return err
	}
	lines := nonEmptyLines(res.out)
	if len(lines) != 2 || lines[0] != "directory root:root 755" || lines[1] != "regular file root:root 644" {
		rep.record(name, false, "installed unit ownership or mode drift")
		return &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: installed unit ownership or mode drift", name)}
	}
	return nil
}

func (a *Adapter) probeMCPBrokerUnit(ctx context.Context, rep *MCPBrokerInstallReport, name string, want []byte) (present, exact bool, retErr error) {
	res, err := a.installProbe(ctx, "sudo", "-n", "stat", "-c", "%F %U:%G %a", "/etc/systemd/system", TorioMCPBrokerUnitPath)
	if err != nil {
		return false, false, err
	}
	lines := nonEmptyLines(res.out)
	if len(lines) == 0 || lines[0] != "directory root:root 755" {
		rep.record(name, false, "could not inspect system unit path")
		return false, false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: privileged stat probe did not establish its control path", name)}
	}
	if len(lines) == 1 {
		return false, false, nil
	}
	if len(lines) != 2 {
		return false, false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: unparseable system unit stat", name)}
	}
	content, err := a.installProbe(ctx, "sudo", "-n", "cat", TorioMCPBrokerUnitPath)
	if err != nil {
		return true, false, err
	}
	if content.exit != 0 {
		return true, false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: could not read installed system unit", name)}
	}
	return true, lines[1] == "regular file root:root 644" && bytes.Equal([]byte(content.out), want), nil
}

func (a *Adapter) mcpBrokerUnitEnabled(ctx context.Context, rep *MCPBrokerInstallReport, name string) (bool, error) {
	res, err := a.installProbe(ctx, "sudo", "-n", "systemctl", "is-enabled", TorioMCPBrokerUnitName)
	if err != nil {
		return false, err
	}
	state := res.trimmed()
	if res.exit == 0 && state == "enabled" {
		return true, nil
	}
	if res.exit == 1 && state == "disabled" {
		return false, nil
	}
	rep.record(name, false, "could not establish whether broker unit is enabled")
	return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
		Err: fmt.Errorf("%s: unexpected is-enabled result %q (exit %d)", name, state, res.exit)}
}

func (a *Adapter) mcpBrokerUnitActive(ctx context.Context, rep *MCPBrokerInstallReport, name string) (bool, error) {
	res, err := a.installProbe(ctx, "sudo", "-n", "systemctl", "is-active", TorioMCPBrokerUnitName)
	if err != nil {
		return false, err
	}
	state := res.trimmed()
	if res.exit == 0 && state == "active" {
		return true, nil
	}
	if res.exit == 3 && (state == "inactive" || state == "failed") {
		return false, nil
	}
	rep.record(name, false, "could not establish whether broker unit is active")
	return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
		Err: fmt.Errorf("%s: unexpected is-active result %q (exit %d)", name, state, res.exit)}
}

func (a *Adapter) verifyMCPRuntimeDirectory(ctx context.Context, rep *MCPBrokerInstallReport) error {
	const name = "install:runtime_directory"
	res, err := a.installProbe(ctx, "sudo", "-n", "stat", "-c", "%F %U:%G %a", "/run", TorioMCPSocketDir)
	if err != nil {
		return err
	}
	lines := nonEmptyLines(res.out)
	if len(lines) != 2 || lines[0] != "directory root:root 755" || lines[1] != "directory torio-mcp:torio-mcp-clients 750" {
		rep.record(name, false, "expected torio-mcp:torio-mcp-clients 750")
		return &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: runtime directory ownership or mode drift", name)}
	}
	rep.record(name, true, "torio-mcp:torio-mcp-clients 750")
	return nil
}
