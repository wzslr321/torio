package lima

import (
	"context"
	"fmt"
	"strings"
)

const mcpInstallOp = "mcp_install"

// TorioMCPPolicyDir holds one JSON policy document per service. It is
// root-owned and world-readable on purpose: ADR-0022 makes the grant legible to
// everyone, including the agent, while the credentials it authorizes stay
// unreadable. An agent that can see exactly what it is allowed to do — and
// cannot change it — is the whole transparency claim.
const TorioMCPPolicyDir = "/etc/torio-mcp/policy.d"

// brokerLoginShell is deliberately a nologin shell. The broker runs as a
// service and the operator's only interaction with this identity is a fixed
// command run through it, so an interactive shell would be authority nobody
// needs.
const brokerLoginShell = "/usr/sbin/nologin"

// MCPBrokerInstallReport is the structured outcome of provisioning the broker
// boundary. Changed reports whether the guest was actually modified, so a re-run
// is visibly a no-op rather than an indistinguishable success.
type MCPBrokerInstallReport struct {
	Instance string
	Changed  bool
	// RestartRequired is set when hermes newly joined the client group. A
	// long-running process does not gain a group because the group database
	// changed under it, so the always-on backend keeps its old credentials until
	// it is restarted.
	RestartRequired bool
	Checks          []CheckResult
}

func (r *MCPBrokerInstallReport) record(name string, ok bool, detail string) {
	r.Checks = append(r.Checks, CheckResult{Name: name, OK: ok, Detail: boundDetail(detail)})
}

// ProvisionMCPBroker installs and verifies only the credential-custody
// boundary. The daemon is deliberately not installed or activated until its
// upstream transport and OAuth lifecycle have their own accepted contract.
func (a *Adapter) ProvisionMCPBroker(ctx context.Context) (MCPBrokerInstallReport, error) {
	rep := MCPBrokerInstallReport{Instance: InstanceName}

	groupChanged, err := a.ensureBrokerClientsGroup(ctx, &rep)
	rep.Changed = rep.Changed || groupChanged
	if err != nil {
		return rep, err
	}
	userChanged, err := a.ensureBrokerUser(ctx, &rep)
	rep.Changed = rep.Changed || userChanged
	if err != nil {
		return rep, err
	}
	brokerClientChanged, err := a.ensureBrokerIsClient(ctx, &rep)
	rep.Changed = rep.Changed || brokerClientChanged
	if err != nil {
		return rep, err
	}
	homeChanged, err := a.ensureBrokerHome(ctx, &rep)
	rep.Changed = rep.Changed || homeChanged
	if err != nil {
		return rep, err
	}
	clientChanged, err := a.ensureHermesIsClient(ctx, &rep)
	rep.Changed = rep.Changed || clientChanged
	rep.RestartRequired = rep.RestartRequired || clientChanged
	if err != nil {
		return rep, err
	}
	policyChanged, err := a.ensurePolicyDir(ctx, &rep)
	rep.Changed = rep.Changed || policyChanged
	if err != nil {
		return rep, err
	}

	verify := MCPBrokerReport{Instance: InstanceName}
	for _, step := range brokerIdentitySteps(a) {
		if err := step(ctx, &verify); err != nil {
			rep.Checks = append(rep.Checks, verify.Checks...)
			return rep, err
		}
	}
	if err := a.verifyPolicyDocuments(ctx, &verify); err != nil {
		rep.Checks = append(rep.Checks, verify.Checks...)
		return rep, err
	}
	rep.Checks = append(rep.Checks, verify.Checks...)
	return rep, nil
}

// InstallMCPBroker provisions the guest identities and directories the broker
// boundary is made of, then proves the result rather than trusting the exit
// codes of the commands that produced it.
//
// It creates one unprivileged identity, one client group, one 0700 home, one
// root-owned policy directory, two root-owned guest binaries and one validated
// system unit. It grants nothing beyond the client group. It never adds
// torio-mcp to TorioProjectsGroup (that would put the credential owner inside
// the project workspace) and never adds hermes to the torio-mcp group (that
// would hand the agent the credentials). Those are the two mistakes that would
// leave every other check green while voiding the decision, so they are absent
// here and asserted against in tests.
//
// It deliberately does NOT gate on credentials still sitting under the Hermes
// profile. They are exactly what the broker exists to end, but refusing to
// install while they are there is a deadlock: the operator cannot build the
// thing they are supposed to migrate to. That ongoing invariant belongs to
// `mcp status`.
func (a *Adapter) InstallMCPBroker(ctx context.Context) (MCPBrokerInstallReport, error) {
	rep := MCPBrokerInstallReport{Instance: InstanceName}
	binaries, err := a.loadMCPGuestBinaries()
	if err != nil {
		return rep, err
	}

	groupChanged, err := a.ensureBrokerClientsGroup(ctx, &rep)
	rep.Changed = rep.Changed || groupChanged
	if err != nil {
		return rep, err
	}
	userChanged, err := a.ensureBrokerUser(ctx, &rep)
	rep.Changed = rep.Changed || userChanged
	if err != nil {
		return rep, err
	}
	brokerClientChanged, err := a.ensureBrokerIsClient(ctx, &rep)
	rep.Changed = rep.Changed || brokerClientChanged
	if err != nil {
		return rep, err
	}
	homeChanged, err := a.ensureBrokerHome(ctx, &rep)
	rep.Changed = rep.Changed || homeChanged
	if err != nil {
		return rep, err
	}
	clientChanged, err := a.ensureHermesIsClient(ctx, &rep)
	rep.Changed = rep.Changed || clientChanged
	rep.RestartRequired = rep.RestartRequired || clientChanged
	if err != nil {
		return rep, err
	}
	policyChanged, err := a.ensurePolicyDir(ctx, &rep)
	rep.Changed = rep.Changed || policyChanged
	if err != nil {
		return rep, err
	}
	binariesChanged := false
	brokerDigest := ""
	for _, bin := range binaries {
		if bin.target == TorioMCPBrokerPath {
			brokerDigest = bin.digest
		}
		changed, err := a.ensureMCPGuestBinary(ctx, &rep, bin)
		if err != nil {
			return rep, err
		}
		binariesChanged = binariesChanged || changed
		rep.Changed = rep.Changed || changed
	}

	// Prove the identity boundary before activating the service. A clean exit
	// from useradd is not evidence that the resulting home is unreadable, and a
	// daemon must not start on an unverified custody boundary.
	verify := MCPBrokerReport{Instance: InstanceName}
	for _, step := range brokerIdentitySteps(a) {
		if err := step(ctx, &verify); err != nil {
			rep.Checks = append(rep.Checks, verify.Checks...)
			return rep, err
		}
	}
	if err := a.verifyPolicyDocuments(ctx, &verify); err != nil {
		rep.Checks = append(rep.Checks, verify.Checks...)
		return rep, err
	}
	rep.Checks = append(rep.Checks, verify.Checks...)

	unitChanged, err := a.ensureMCPBrokerUnit(ctx, &rep, brokerDigest, verify.policyDigest)
	if err != nil {
		return rep, err
	}
	rep.Changed = rep.Changed || unitChanged
	socketReport := MCPBrokerReport{Instance: InstanceName, policyServices: verify.policyServices, policyDigest: verify.policyDigest}
	if err := a.verifyBrokerSockets(ctx, &socketReport); err != nil {
		rep.Checks = append(rep.Checks, socketReport.Checks...)
		return rep, err
	}
	rep.Checks = append(rep.Checks, socketReport.Checks...)

	rep.Changed = rep.Changed || groupChanged || userChanged || brokerClientChanged || homeChanged || clientChanged || policyChanged || binariesChanged || unitChanged
	rep.RestartRequired = rep.RestartRequired || clientChanged
	return rep, nil
}

// installProbe runs a fixed guest argv and refuses to treat truncated output as
// an answer.
func (a *Adapter) installProbe(ctx context.Context, argv ...string) (result, error) {
	res, err := a.SSH(ctx, argv)
	if err != nil {
		return result{}, err
	}
	if res.StdoutTruncated || res.StderrTruncated {
		return result{}, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("guest output was truncated; a truncated probe is not proof")}
	}
	return result{exit: res.ExitCode, out: string(res.Stdout)}, nil
}

// installMutate runs a fixed reconcile argv and fails closed on a non-zero exit.
func (a *Adapter) installMutate(ctx context.Context, rep *MCPBrokerInstallReport, name, detail string, argv ...string) error {
	res, err := a.SSH(ctx, argv)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		rep.record(name, false, detail+" failed")
		return &Error{Op: mcpInstallOp, Kind: KindCommandFailed,
			Err: fmt.Errorf("%s: %s exited %d", name, detail, res.ExitCode)}
	}
	return nil
}

func (a *Adapter) ensureBrokerClientsGroup(ctx context.Context, rep *MCPBrokerInstallReport) (bool, error) {
	const name = "install:clients_group"
	res, err := a.installProbe(ctx, "getent", "group", TorioMCPClientsGroup)
	if err != nil {
		return false, err
	}
	if res.exit == 0 && res.trimmed() != "" {
		rep.record(name, true, "present")
		return false, nil
	}
	if err := a.installMutate(ctx, rep, name, "groupadd", "sudo", "-n", "groupadd", "--system", TorioMCPClientsGroup); err != nil {
		return false, err
	}
	rep.record(name, true, "created")
	return true, nil
}

func (a *Adapter) ensureBrokerUser(ctx context.Context, rep *MCPBrokerInstallReport) (bool, error) {
	const name = "install:broker_user"
	res, err := a.installProbe(ctx, "id", "-u", TorioMCPUser)
	if err != nil {
		return false, err
	}
	if res.exit == 0 && res.trimmed() != "" {
		rep.record(name, true, "present")
		return false, nil
	}
	// --system: this is a service identity, not a person. No supplementary
	// groups are passed at all, so the account starts with exactly its own.
	if err := a.installMutate(ctx, rep, name, "useradd",
		"sudo", "-n", "useradd", "--system", "--user-group", "--create-home",
		"--home-dir", TorioMCPHome, "--shell", brokerLoginShell, TorioMCPUser); err != nil {
		return false, err
	}
	rep.record(name, true, "created")
	return true, nil
}

// ensureBrokerIsClient lets the broker hand each socket to the client group.
// chown(2) rejects a target group outside the creating process's memberships,
// even when the process owns the socket, so omitting this membership leaves the
// daemon unable to publish its first service.
func (a *Adapter) ensureBrokerIsClient(ctx context.Context, rep *MCPBrokerInstallReport) (bool, error) {
	const name = "install:broker_client"
	res, err := a.installProbe(ctx, "id", "-nG", TorioMCPUser)
	if err != nil {
		return false, err
	}
	if res.exit != 0 {
		rep.record(name, false, "cannot read broker group membership")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: cannot read broker group membership", name)}
	}
	if hasGroup(res.out, TorioMCPClientsGroup) {
		rep.record(name, true, "member")
		return false, nil
	}
	if err := a.installMutate(ctx, rep, name, "usermod",
		"sudo", "-n", "usermod", "-aG", TorioMCPClientsGroup, TorioMCPUser); err != nil {
		return false, err
	}
	verified, err := a.installProbe(ctx, "id", "-nG", TorioMCPUser)
	if err != nil {
		return true, err
	}
	if verified.exit != 0 || !hasGroup(verified.out, TorioMCPClientsGroup) {
		rep.record(name, false, "membership missing after usermod")
		return true, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: broker is not in the client group after usermod", name)}
	}
	rep.record(name, true, "added")
	return true, nil
}

// ensureBrokerHome brings the credential store to 0700 owned by the broker.
// useradd leaves a world-traversable default, which would defeat the point, so
// the mode is reconciled explicitly rather than inherited.
func (a *Adapter) ensureBrokerHome(ctx context.Context, rep *MCPBrokerInstallReport) (bool, error) {
	const name = "install:broker_home"

	st, err := a.installProbe(ctx, "sudo", "-n", "stat", "-c", "%F", TorioMCPHome)
	if err != nil {
		return false, err
	}
	if st.exit != 0 || st.trimmed() != "directory" {
		rep.record(name, false, "broker home is missing")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: %s is not a directory after provisioning", name, TorioMCPHome)}
	}

	og, err := a.installProbe(ctx, "sudo", "-n", "stat", "-c", "%U:%G %a", TorioMCPHome)
	if err != nil {
		return false, err
	}
	owner, group, mode, ok := parseStatOwnership(og.out)
	if og.exit != 0 || !ok {
		rep.record(name, false, "unreadable ownership/mode")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: could not read ownership/mode of %s", name, TorioMCPHome)}
	}

	changed := false
	if owner != TorioMCPUser || group != TorioMCPUser {
		if err := a.installMutate(ctx, rep, name, "chown",
			"sudo", "-n", "chown", TorioMCPUser+":"+TorioMCPUser, TorioMCPHome); err != nil {
			return changed, err
		}
		changed = true
	}
	if !modeMatches(torioMCPHomeSpec, mode) {
		if err := a.installMutate(ctx, rep, name, "chmod",
			"sudo", "-n", "chmod", "700", TorioMCPHome); err != nil {
			return changed, err
		}
		changed = true
	}
	if changed {
		rep.record(name, true, "reconciled to 700")
		return true, nil
	}
	rep.record(name, true, fmt.Sprintf("%s:%s %s", owner, group, mode))
	return false, nil
}

// ensureHermesIsClient grants the agent identity the one privilege it gets: the
// right to open the broker socket. -aG appends, so no existing membership is
// disturbed; passing -G here would silently strip hermes out of
// TorioProjectsGroup and break the workspace.
func (a *Adapter) ensureHermesIsClient(ctx context.Context, rep *MCPBrokerInstallReport) (bool, error) {
	const name = "install:hermes_client"
	res, err := a.installProbe(ctx, "id", "-nG", HermesUser)
	if err != nil {
		return false, err
	}
	if res.exit != 0 {
		rep.record(name, false, "cannot read hermes group membership")
		return false, &Error{Op: mcpInstallOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("%s: cannot read hermes group membership", name)}
	}
	if hasGroup(res.out, TorioMCPClientsGroup) {
		rep.record(name, true, "member")
		return false, nil
	}
	if err := a.installMutate(ctx, rep, name, "usermod",
		"sudo", "-n", "usermod", "-aG", TorioMCPClientsGroup, HermesUser); err != nil {
		return false, err
	}
	rep.record(name, true, "added")
	return true, nil
}

func (a *Adapter) ensurePolicyDir(ctx context.Context, rep *MCPBrokerInstallReport) (bool, error) {
	const name = "install:policy_dir"
	res, err := a.installProbe(ctx, "sudo", "-n", "stat", "-c", "%F", TorioMCPPolicyDir)
	if err != nil {
		return false, err
	}
	if res.exit == 0 && strings.TrimSpace(res.out) == "directory" {
		rep.record(name, true, "present")
		return false, nil
	}
	// 0755 root-owned: readable by the agent, writable only by root. The grant
	// must be legible to the party it constrains and changeable only by the party
	// that issues it.
	if err := a.installMutate(ctx, rep, name, "install -d",
		"sudo", "-n", "install", "-d", "-o", "root", "-g", "root", "-m", "0755", TorioMCPPolicyDir); err != nil {
		return false, err
	}
	rep.record(name, true, "created")
	return true, nil
}
