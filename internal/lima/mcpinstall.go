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

// InstallMCPBroker provisions the guest identities and directories the broker
// boundary is made of, then proves the result rather than trusting the exit
// codes of the commands that produced it.
//
// It is narrow by construction. It creates one unprivileged identity, one client
// group, one 0700 home and one root-owned policy directory — and it grants
// nothing else. It never adds torio-mcp to TorioProjectsGroup (that would put
// the credential owner inside the project workspace) and never adds hermes to
// the torio-mcp group (that would hand the agent the credentials). Those are the
// two mistakes that would leave every other check green while voiding the
// decision, so they are absent here and asserted against in tests.
//
// It deliberately does NOT gate on credentials still sitting under the Hermes
// profile. They are exactly what the broker exists to end, but refusing to
// install while they are there is a deadlock: the operator cannot build the
// thing they are supposed to migrate to. That ongoing invariant belongs to
// `mcp status`.
func (a *Adapter) InstallMCPBroker(ctx context.Context) (MCPBrokerInstallReport, error) {
	rep := MCPBrokerInstallReport{Instance: InstanceName}

	groupChanged, err := a.ensureBrokerClientsGroup(ctx, &rep)
	if err != nil {
		return rep, err
	}
	userChanged, err := a.ensureBrokerUser(ctx, &rep)
	if err != nil {
		return rep, err
	}
	homeChanged, err := a.ensureBrokerHome(ctx, &rep)
	if err != nil {
		return rep, err
	}
	clientChanged, err := a.ensureHermesIsClient(ctx, &rep)
	if err != nil {
		return rep, err
	}
	policyChanged, err := a.ensurePolicyDir(ctx, &rep)
	if err != nil {
		return rep, err
	}

	rep.Changed = groupChanged || userChanged || homeChanged || clientChanged || policyChanged
	rep.RestartRequired = clientChanged

	// Prove the boundary rather than trusting the reconcile. A clean exit from
	// useradd is not evidence that the resulting home is unreadable.
	verify := MCPBrokerReport{Instance: InstanceName}
	for _, step := range brokerIdentitySteps(a) {
		if err := step(ctx, &verify); err != nil {
			rep.Checks = append(rep.Checks, verify.Checks...)
			return rep, err
		}
	}
	rep.Checks = append(rep.Checks, verify.Checks...)
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
		"sudo", "-n", "useradd", "--system", "--create-home",
		"--home-dir", TorioMCPHome, "--shell", brokerLoginShell, TorioMCPUser); err != nil {
		return false, err
	}
	rep.record(name, true, "created")
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
			return false, err
		}
		changed = true
	}
	if !modeMatches(torioMCPHomeSpec, mode) {
		if err := a.installMutate(ctx, rep, name, "chmod",
			"sudo", "-n", "chmod", "700", TorioMCPHome); err != nil {
			return false, err
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
