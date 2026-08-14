package lima

import (
	"context"
	"fmt"
	"path"

	"github.com/wzslr321/torio/internal/backend"
)

const torioMCPOAuthDir = TorioMCPHome + "/oauth"

// MCPBrokerActivationReport distinguishes a completed login from a runnable
// complete policy. A multi-service policy is activated only after every
// service has its own private OAuth document.
type MCPBrokerActivationReport struct {
	Activated bool
	Pending   int
}

func (a *Adapter) ActivateMCPBroker(ctx context.Context, identity backend.Identity) (MCPBrokerActivationReport, error) {
	if err := validateMCPBackendIdentity(identity); err != nil {
		return MCPBrokerActivationReport{}, err
	}
	verify := MCPBrokerReport{Instance: InstanceName}
	if err := a.verifyPolicyDocuments(ctx, &verify); err != nil {
		return MCPBrokerActivationReport{}, err
	}
	return a.activateMCPBrokerForGrant(ctx, verify.Policy)
}

func (a *Adapter) activateMCPBrokerForGrant(ctx context.Context, grant PolicyGrant) (MCPBrokerActivationReport, error) {
	rep := MCPBrokerActivationReport{}
	pending, err := a.mcpOAuthPending(ctx, grant)
	if err != nil {
		return rep, err
	}
	rep.Pending = pending
	if rep.Pending > 0 {
		return rep, nil
	}
	enable, err := a.SSH(ctx, []string{"sudo", "-n", "systemctl", "enable", TorioMCPBrokerUnitName})
	if err != nil {
		return rep, err
	}
	if enable.ExitCode != 0 {
		return rep, &Error{Op: mcpInstallOp, Kind: KindCommandFailed, Err: fmt.Errorf("broker enable exited %d", enable.ExitCode)}
	}
	restart, err := a.SSH(ctx, []string{"sudo", "-n", "systemctl", "restart", TorioMCPBrokerUnitName})
	if err != nil {
		return rep, err
	}
	if restart.ExitCode != 0 {
		return rep, &Error{Op: mcpInstallOp, Kind: KindCommandFailed, Err: fmt.Errorf("broker activation exited %d", restart.ExitCode)}
	}
	active, err := a.installProbe(ctx, "sudo", "-n", "systemctl", "is-active", TorioMCPBrokerUnitName)
	if err != nil {
		return rep, err
	}
	if active.exit != 0 || active.trimmed() != "active" {
		return rep, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("broker did not become active")}
	}
	rep.Activated = true
	return rep, nil
}

func (a *Adapter) reconcileMCPRuntimeAfterInstall(ctx context.Context, grant PolicyGrant, transportChanged bool, install *MCPBrokerInstallReport) (bool, error) {
	pending, err := a.mcpOAuthPending(ctx, grant)
	if err != nil {
		return false, err
	}
	enabled, err := a.installProbe(ctx, "sudo", "-n", "systemctl", "is-enabled", TorioMCPBrokerUnitName)
	if err != nil {
		return false, err
	}
	active, err := a.installProbe(ctx, "sudo", "-n", "systemctl", "is-active", TorioMCPBrokerUnitName)
	if err != nil {
		return false, err
	}
	isEnabled, ok := exactSystemdState(enabled, 0, "enabled", 1, "disabled")
	if !ok {
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("broker enabled state was not verifiable")}
	}
	isActive, ok := exactSystemdState(active, 0, "active", 3, "inactive")
	if !ok {
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("broker active state was not verifiable")}
	}
	if pending > 0 {
		if isEnabled || isActive {
			if err := a.installMutate(ctx, install, "install:broker_runtime", "disable pending broker",
				"sudo", "-n", "systemctl", "disable", "--now", TorioMCPBrokerUnitName); err != nil {
				return false, err
			}
			install.record("install:broker_runtime", true, fmt.Sprintf("stopped; %d login(s) pending", pending))
			return true, nil
		}
		install.record("install:broker_runtime", true, fmt.Sprintf("dormant; %d login(s) pending", pending))
		return false, nil
	}

	changed := false
	if !isEnabled {
		if err := a.installMutate(ctx, install, "install:broker_runtime", "enable broker",
			"sudo", "-n", "systemctl", "enable", TorioMCPBrokerUnitName); err != nil {
			return changed, err
		}
		changed = true
	}
	restartNeeded := !isActive || transportChanged
	if isActive && !restartNeeded {
		digest, err := a.installProbe(ctx, "sudo", "-n", "cat", torioMCPPolicyDigestPath)
		if err != nil {
			return changed, err
		}
		restartNeeded = digest.exit != 0 || digest.trimmed() != grant.Digest
	}
	if restartNeeded {
		if err := a.installMutate(ctx, install, "install:broker_runtime", "restart broker",
			"sudo", "-n", "systemctl", "restart", TorioMCPBrokerUnitName); err != nil {
			return changed, err
		}
		changed = true
		verified, err := a.installProbe(ctx, "sudo", "-n", "systemctl", "is-active", TorioMCPBrokerUnitName)
		if err != nil {
			return changed, err
		}
		if verified.exit != 0 || verified.trimmed() != "active" {
			return changed, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("broker did not become active after install")}
		}
	}
	install.record("install:broker_runtime", true, "active with complete OAuth state")
	return changed, nil
}

func exactSystemdState(result result, yesExit int, yesText string, noExit int, noText string) (bool, bool) {
	switch {
	case result.exit == yesExit && result.trimmed() == yesText:
		return true, true
	case result.exit == noExit && result.trimmed() == noText:
		return false, true
	default:
		return false, false
	}
}

func (a *Adapter) mcpOAuthPending(ctx context.Context, grant PolicyGrant) (int, error) {
	present, metadata, err := a.privateMCPPathMetadata(ctx, torioMCPOAuthDir)
	if err != nil {
		return 0, err
	}
	if !present {
		return len(grant.Services), nil
	}
	if metadata != "directory torio-mcp:torio-mcp 700" {
		return 0, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("private OAuth directory ownership or mode has drifted")}
	}
	pending := 0
	for _, service := range grant.Services {
		if err := ValidateServiceName(service.Name); err != nil {
			return 0, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("policy service is invalid")}
		}
		present, metadata, err := a.privateMCPPathMetadata(ctx, path.Join(torioMCPOAuthDir, service.Name+".json"))
		if err != nil {
			return 0, err
		}
		if !present {
			pending++
			continue
		}
		if metadata != "regular file torio-mcp:torio-mcp 600" {
			return 0, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("private OAuth session ownership or mode has drifted")}
		}
	}
	return pending, nil
}

func (a *Adapter) privateMCPPathMetadata(ctx context.Context, target string) (bool, string, error) {
	res, err := a.installProbe(ctx, "sudo", "-n", "stat", "-c", "%F %U:%G %a", statControlPath, target)
	if err != nil {
		return false, "", err
	}
	lines := nonEmptyLines(res.out)
	if len(lines) == 0 || lines[0] != "directory root:root 755" {
		return false, "", &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("private OAuth metadata probe was not usable")}
	}
	switch len(lines) {
	case 1:
		return false, "", nil
	case 2:
		return true, lines[1], nil
	default:
		return false, "", &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("private OAuth metadata probe returned an invalid shape")}
	}
}

func validateMCPBackendIdentity(identity backend.Identity) error {
	if identity.GuestUser == "" {
		return &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("selected backend has no guest identity")}
	}
	switch identity.Name {
	case "hermes", "claude-code", "codex":
		return nil
	default:
		return &Error{Op: mcpInstallOp, Kind: KindVerificationFailed, Err: fmt.Errorf("backend %q has no MCP transport contract", identity.Name)}
	}
}
