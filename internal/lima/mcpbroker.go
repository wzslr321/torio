package lima

import (
	"context"
	"fmt"
	"strings"
)

const mcpBrokerOp = "mcp_broker"

// The MCP broker boundary (ADR-0022). Torio reaches MCP servers through a
// broker that runs under its own guest identity, so no upstream credential ever
// exists under the identity the agent has a shell as.
//
// The separation is the whole decision. Hermes stores MCP OAuth tokens under
// $HERMES_HOME, and its own agent/file_safety.py states that the read denylist
// protecting them "is NOT a security boundary" because the terminal tool runs as
// the same OS user. Moving the credentials to an identity `hermes` cannot read
// is what turns the claim into one the kernel enforces rather than one the agent
// is asked to respect.
const (
	// TorioMCPUser owns every upstream MCP credential on the guest. It is
	// unprivileged, has no sudo, and is deliberately outside TorioProjectsGroup:
	// it must not be able to reach project checkouts, and hermes must not be able
	// to reach its home.
	TorioMCPUser = "torio-mcp"
	// TorioMCPHome holds the broker's credential store. 0700 is not a
	// convenience default here — it is the custody boundary.
	TorioMCPHome = "/home/torio-mcp"
	// TorioMCPClientsGroup is the entire privilege hermes is granted: permission
	// to open a connection to the broker's socket. It conveys no read access to
	// TorioMCPHome and no authority over the broker's policy.
	TorioMCPClientsGroup = "torio-mcp-clients"
	// HermesMCPTokensPath is where Hermes puts MCP credentials when it manages
	// them itself. On a Torio-managed guest it must hold nothing: content here
	// means somebody ran `hermes mcp add` directly and put a token back under the
	// agent's own identity.
	HermesMCPTokensPath = HermesProfilePath + "/mcp-tokens"
)

// torioMCPHomeSpec is the required state of the broker's home. Unlike the
// Hermes paths in bootstrapRequiredPaths, nothing here may be group-readable:
// allowStricter is deliberately absent so a *looser* mode fails and the check
// cannot be satisfied by widening.
var torioMCPHomeSpec = bootstrapPathSpec{
	path:  TorioMCPHome,
	owner: TorioMCPUser,
	group: TorioMCPUser,
	modes: []string{"700", "0700"},
}

// MCPBrokerReport is the structured outcome of verifying the broker boundary.
// On success every check is OK; on failure it carries the checks recorded up to
// and including the failing one, so the CLI can surface a precise, redacted
// marker rather than a generic error.
type MCPBrokerReport struct {
	Instance string
	Checks   []CheckResult
}

func (r *MCPBrokerReport) record(name string, ok bool, detail string) {
	r.Checks = append(r.Checks, CheckResult{Name: name, OK: ok, Detail: boundDetail(detail)})
}

// VerifyMCPBroker proves the ADR-0022 boundary on the guest. It modifies
// nothing: every drift is reported as a stable marker and fails closed, never
// repaired in place — the same contract the rest of the adapter keeps, and the
// only one that makes a security claim worth printing.
func (a *Adapter) VerifyMCPBroker(ctx context.Context) (MCPBrokerReport, error) {
	rep := MCPBrokerReport{Instance: InstanceName}

	steps := append(brokerIdentitySteps(a), a.verifyNoHermesMCPTokens, a.verifyBrokerSockets)
	for _, step := range steps {
		if err := step(ctx, &rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// brokerIdentitySteps prove the separation itself: the identities exist, the
// credential store is reachable by nobody but its owner, and hermes may open the
// socket without being able to read what is behind it.
//
// They are named apart from the full status set because install and status ask
// different questions. Install proves what install created. Status additionally
// asks whether a credential has since reappeared under the agent's own identity
// — an ongoing invariant, not a postcondition of provisioning, and one that
// would deadlock the installer if it gated it (see InstallMCPBroker).
func brokerIdentitySteps(a *Adapter) []func(context.Context, *MCPBrokerReport) error {
	return []func(context.Context, *MCPBrokerReport) error{
		a.verifyBrokerUser,
		a.verifyBrokerClientsGroup,
		a.verifyHermesIsBrokerClient,
		a.verifyHermesNotBrokerOwner,
		a.verifyBrokerHome,
	}
}

func (a *Adapter) brokerProbe(ctx context.Context, rep *MCPBrokerReport, name string, argv ...string) (result, error) {
	res, err := a.SSH(ctx, argv)
	if err != nil {
		return result{}, err
	}
	if res.StdoutTruncated || res.StderrTruncated {
		return result{}, a.brokerFailed(rep, name, "guest output was truncated", "re-run; a truncated probe is not proof")
	}
	return result{exit: res.ExitCode, out: string(res.Stdout)}, nil
}

// result is the small, already-bounded shape the broker checks read. Keeping it
// local means no check can accidentally reach for a raw output blob.
type result struct {
	exit int
	out  string
}

func (r result) trimmed() string { return strings.TrimSpace(r.out) }

// brokerFailed records drift: the broker is there, but a boundary this decision
// depends on does not hold. It fails closed as a verification failure.
func (a *Adapter) brokerFailed(rep *MCPBrokerReport, name, detail, remediation string) *Error {
	rep.record(name, false, detail)
	return &Error{Op: mcpBrokerOp, Kind: KindVerificationFailed, Err: fmt.Errorf("%s: %s (%s)", name, detail, remediation)}
}

// brokerMissing records that the broker was never provisioned. It is a separate
// classification from brokerFailed on purpose: both fail closed, but only drift
// means somebody broke a guarantee. Telling an operator who has simply not run
// the installer that a custody boundary was violated would train them to ignore
// the message that matters.
func (a *Adapter) brokerMissing(rep *MCPBrokerReport, name, detail, remediation string) *Error {
	rep.record(name, false, detail)
	return &Error{Op: mcpBrokerOp, Kind: KindNotFound, Err: fmt.Errorf("%s: %s (%s)", name, detail, remediation)}
}

func (a *Adapter) verifyBrokerUser(ctx context.Context, rep *MCPBrokerReport) error {
	const name = "broker_user"
	res, err := a.brokerProbe(ctx, rep, name, "id", "-u", TorioMCPUser)
	if err != nil {
		return err
	}
	uid := res.trimmed()
	if res.exit != 0 || uid == "" {
		return a.brokerMissing(rep, name, "torio-mcp user not found", "run `torio mcp install` to provision the broker identity")
	}
	rep.record(name, true, "uid="+uid)
	return nil
}

func (a *Adapter) verifyBrokerClientsGroup(ctx context.Context, rep *MCPBrokerReport) error {
	const name = "broker_clients_group"
	res, err := a.brokerProbe(ctx, rep, name, "getent", "group", TorioMCPClientsGroup)
	if err != nil {
		return err
	}
	if res.exit != 0 || res.trimmed() == "" {
		return a.brokerMissing(rep, name, "group torio-mcp-clients not found", "run `torio mcp install` to provision the broker identity")
	}
	rep.record(name, true, TorioMCPClientsGroup)
	return nil
}

func (a *Adapter) verifyHermesIsBrokerClient(ctx context.Context, rep *MCPBrokerReport) error {
	const name = "hermes_broker_client"
	res, err := a.brokerProbe(ctx, rep, name, "id", "-nG", HermesUser)
	if err != nil {
		return err
	}
	if res.exit != 0 {
		return a.brokerFailed(rep, name, "cannot read hermes group membership", "confirm the hermes user exists on the guest")
	}
	if !hasGroup(res.out, TorioMCPClientsGroup) {
		return a.brokerFailed(rep, name, "hermes is not in torio-mcp-clients", "add hermes to torio-mcp-clients; without it the agent cannot reach the broker socket at all")
	}
	rep.record(name, true, "member")
	return nil
}

// verifyHermesNotBrokerOwner is the custody invariant. Membership in the
// torio-mcp group would make the broker's home reachable by the identity the
// agent has a shell as, which is precisely the arrangement ADR-0022 exists to
// end. It is checked separately from the client-group membership so the two
// failures are never confused: one means the boundary is broken, the other only
// that the plumbing is.
func (a *Adapter) verifyHermesNotBrokerOwner(ctx context.Context, rep *MCPBrokerReport) error {
	const name = "hermes_not_broker_owner"
	res, err := a.brokerProbe(ctx, rep, name, "id", "-nG", HermesUser)
	if err != nil {
		return err
	}
	if res.exit != 0 {
		return a.brokerFailed(rep, name, "cannot read hermes group membership", "confirm the hermes user exists on the guest")
	}
	if hasGroup(res.out, TorioMCPUser) {
		return a.brokerFailed(rep, name, "hermes is in the torio-mcp group",
			"remove hermes from torio-mcp; membership makes every broker credential readable by the agent identity (ADR-0022)")
	}
	rep.record(name, true, "not a member")
	return nil
}

func (a *Adapter) verifyBrokerHome(ctx context.Context, rep *MCPBrokerReport) error {
	name := "path:" + torioMCPHomeSpec.path

	st, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%F", torioMCPHomeSpec.path)
	if err != nil {
		return err
	}
	if st.exit != 0 || st.trimmed() != "directory" {
		return a.brokerMissing(rep, name, "not a directory", "run `torio mcp install` to provision the broker credential store")
	}

	og, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%U:%G %a", torioMCPHomeSpec.path)
	if err != nil {
		return err
	}
	if og.exit != 0 {
		return a.brokerFailed(rep, name, "could not read ownership/mode", "verify the path exists on the guest")
	}
	owner, group, mode, ok := parseStatOwnership(og.out)
	if !ok {
		return a.brokerFailed(rep, name, "unparseable ownership/mode", "verify the path exists on the guest")
	}
	if owner != torioMCPHomeSpec.owner || group != torioMCPHomeSpec.group {
		return a.brokerFailed(rep, name,
			fmt.Sprintf("owner:group %s:%s, want %s:%s", owner, group, torioMCPHomeSpec.owner, torioMCPHomeSpec.group),
			"fix broker home ownership on the guest")
	}
	if !modeMatches(torioMCPHomeSpec, mode) {
		return a.brokerFailed(rep, name,
			fmt.Sprintf("mode %s, want one of %v", mode, torioMCPHomeSpec.modes),
			"the broker credential store must not be readable outside torio-mcp (ADR-0022)")
	}
	rep.record(name, true, fmt.Sprintf("%s:%s %s", owner, group, mode))
	return nil
}

// verifyNoHermesMCPTokens catches the one drift an operator can cause without
// touching Torio at all: running `hermes mcp add` on a managed guest, which
// authenticates upstream and writes the token straight back under the agent's
// own identity.
//
// Presence of the directory is not the finding — Hermes creates it unprompted.
// Content is. The check reports how many credential files it found and never
// their names: the shape of the drift is what the operator needs, and guest
// filenames are not something this surface prints.
func (a *Adapter) verifyNoHermesMCPTokens(ctx context.Context, rep *MCPBrokerReport) error {
	const name = "hermes_mcp_tokens"

	st, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%F", HermesMCPTokensPath)
	if err != nil {
		return err
	}
	if st.exit != 0 {
		// As root the only ordinary reason stat fails here is that the path is
		// not there, which is the desired end state.
		rep.record(name, true, "absent")
		return nil
	}
	if st.trimmed() != "directory" {
		return a.brokerFailed(rep, name, "mcp-tokens exists and is not a directory",
			"inspect the guest by hand; this path is managed by Hermes and should be a directory or absent")
	}

	// -printf x emits one byte per match, so the reply carries a count and no
	// filename ever crosses the boundary.
	found, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "find", HermesMCPTokensPath, "-mindepth", "1", "-type", "f", "-printf", "x")
	if err != nil {
		return err
	}
	if found.exit != 0 {
		return a.brokerFailed(rep, name, "could not enumerate the Hermes MCP token store", "verify the path on the guest")
	}
	if n := len(strings.TrimSpace(found.out)); n > 0 {
		return a.brokerFailed(rep, name,
			fmt.Sprintf("%d credential files under the Hermes profile", n),
			"a credential was created outside the broker (likely `hermes mcp add`); revoke it upstream and re-add the service through `torio mcp`")
	}
	rep.record(name, true, "empty")
	return nil
}
