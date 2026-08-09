package lima

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
)

const mcpBrokerOp = "mcp_broker"

// The MCP broker boundary (ADR-0004). Torio reaches MCP servers through a
// broker that runs under its own guest identity, so no released-path upstream
// credential exists under the identity the agent has a shell as.
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
// AllowStricter is deliberately absent so a *looser* mode fails and the check
// cannot be satisfied by widening.
var torioMCPHomeSpec = backend.PathSpec{
	Path:  TorioMCPHome,
	Owner: TorioMCPUser,
	Group: TorioMCPUser,
	Modes: []string{"700", "0700"},
}

// MCPBrokerReport is the structured outcome of verifying the broker boundary.
// On success every check is OK; on failure it carries the checks recorded up to
// and including the failing one, so the CLI can surface a precise, redacted
// marker rather than a generic error.
type MCPBrokerReport struct {
	Instance  string
	AgentUser string
	Checks    []CheckResult
	// Policy is the grant the verified documents carry. It is populated once
	// verifyPolicyDocuments has proven and parsed them, and is empty before that
	// and on every failure that precedes it.
	Policy PolicyGrant
}

// PolicyGrant is what the guest's policy documents grant, in the shape a report
// renders it: enumerated by service and counted.
//
// ADR-0004 §4 requires a report to be able to state how many granted tools carry
// a write, which is the reason the write flag is mandatory in the document. A
// caller that has to recover that number by parsing an English detail line
// cannot state it; so the count is carried as a count.
type PolicyGrant struct {
	// Digest identifies the effective grant independent of file formatting and
	// tool order. It is the same value a running broker publishes and the value
	// verifyBrokerSockets compares against, so a report and the process enforcing
	// policy can be shown to be describing one grant rather than two.
	Digest string
	// Services is every service the policy speaks for, ordered by name.
	Services []PolicyService
}

// PolicyService is one service's grant, summarised.
//
// The tool names themselves stay out. A report answers what is granted and how
// much of it writes; the question "exactly which tools" is answered by the
// policy document, which ADR-0004 §4 keeps world-readable precisely so that
// nobody has to take a summary's word for it.
type PolicyService struct {
	Name string
	// UpstreamEndpoint is where this service's traffic goes. An operator asking
	// what is granted is also asking where the data lands, and the endpoint is
	// the only place that is written down.
	UpstreamEndpoint string
	// Tools is how many tools the service grants, WriteTools how many of those
	// are write-classified. Both are derived from the same parsed grant, so
	// neither can drift from the other.
	Tools      int
	WriteTools int
}

func (r *MCPBrokerReport) record(name string, ok bool, detail string) {
	r.Checks = append(r.Checks, CheckResult{Name: name, OK: ok, Detail: boundDetail(detail)})
}

// VerifyMCPBroker proves the ADR-0004 boundary on the guest. It modifies
// nothing: every drift is reported as a stable marker and fails closed, never
// repaired in place — the same contract the rest of the adapter keeps, and the
// only one that makes a security claim worth printing.
func (a *Adapter) VerifyMCPBroker(ctx context.Context) (MCPBrokerReport, error) {
	rep := MCPBrokerReport{Instance: InstanceName, AgentUser: HermesUser}

	// Order is custody first, then the two documents that decide what the
	// custody is for, then liveness. A guest whose policy is agent-writable has
	// a broken boundary whether or not anything is listening, so the socket
	// check is not what an operator should hear about first.
	steps := append(brokerIdentitySteps(a),
		a.verifyNoHermesMCPTokens,
		a.verifyPolicyDocuments,
		a.verifyHermesMCPServers,
	)
	for _, step := range steps {
		if err := step(ctx, &rep); err != nil {
			return rep, err
		}
	}
	runtimePresent, err := a.probeMCPRuntimePresence(ctx, &rep)
	if err != nil {
		return rep, err
	}
	if !runtimePresent {
		return rep, nil
	}
	if err := a.verifyMCPBrokerUnit(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.verifyBrokerSockets(ctx, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// VerifyMCPBrokerFor proves the released transport for the selected backend.
// Unlike the legacy Hermes-only verifier it also checks the backend-specific
// client declaration and the OAuth/runtime relationship: a broker with complete
// OAuth state must be active and publishing the exact policy sockets.
func (a *Adapter) VerifyMCPBrokerFor(ctx context.Context, identity backend.Identity) (MCPBrokerReport, error) {
	rep := MCPBrokerReport{Instance: InstanceName, AgentUser: identity.GuestUser}
	if err := validateMCPBackendIdentity(identity); err != nil {
		return rep, err
	}
	for _, step := range brokerIdentityStepsFor(a, identity) {
		if err := step(ctx, &rep); err != nil {
			return rep, err
		}
	}
	if identity.Name == "hermes" {
		if err := a.verifyNoHermesMCPTokens(ctx, &rep); err != nil {
			return rep, err
		}
	}
	if err := a.verifyPolicyDocuments(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.verifyBackendMCPConfig(ctx, &rep, identity.Name); err != nil {
		return rep, err
	}
	pending, err := a.mcpOAuthPending(ctx, rep.Policy)
	if err != nil {
		return rep, err
	}
	if pending > 0 {
		rep.record("oauth_sessions", true, fmt.Sprintf("%d policy service(s) require login", pending))
	} else {
		rep.record("oauth_sessions", true, fmt.Sprintf("%d private session(s), ownership and mode verified", len(rep.Policy.Services)))
	}
	runtimePresent, err := a.probeMCPRuntimePresence(ctx, &rep)
	if err != nil {
		return rep, err
	}
	if pending > 0 {
		if runtimePresent {
			return rep, a.brokerFailed(&rep, "broker_runtime", "runtime exists while policy services lack OAuth state", "stop the broker and complete each `torio mcp login <service>`")
		}
		return rep, nil
	}
	if !runtimePresent {
		return rep, a.brokerMissing(&rep, "broker_runtime", "OAuth state is complete but the broker runtime is absent", "run `torio mcp login <service>` again to activate the unit")
	}
	if err := a.verifyMCPBrokerUnit(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.verifyBrokerSockets(ctx, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// brokerIdentitySteps prove the separation itself: the identities exist, the
// credential store is reachable by nobody but its owner, and the agent may open the
// socket without being able to read what is behind it.
//
// They are named apart from the full status set because install and status ask
// different questions. Install proves what install created. Status additionally
// asks whether a credential has since reappeared under the agent's own identity
// — an ongoing invariant, not a postcondition of provisioning, and one that
// would deadlock the installer if it gated it (see InstallMCPBroker).
func brokerIdentitySteps(a *Adapter) []func(context.Context, *MCPBrokerReport) error {
	return brokerIdentityStepsFor(a, Hermes().Identity())
}

func brokerIdentityStepsFor(a *Adapter, identity backend.Identity) []func(context.Context, *MCPBrokerReport) error {
	return []func(context.Context, *MCPBrokerReport) error{
		func(ctx context.Context, rep *MCPBrokerReport) error {
			return a.verifyBrokerUserFor(ctx, rep, identity.GuestUser)
		},
		a.verifyBrokerClientsGroup,
		func(ctx context.Context, rep *MCPBrokerReport) error {
			return a.verifyAgentIsBrokerClient(ctx, rep, identity.GuestUser)
		},
		func(ctx context.Context, rep *MCPBrokerReport) error {
			return a.verifyAgentNotBrokerOwner(ctx, rep, identity.GuestUser)
		},
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

// statControlPath is a second operand every privileged stat probe carries so
// that the probe establishes its own premise instead of assuming it.
//
// `sudo -n stat <path>` exiting non-zero says nothing on its own: the exit is
// the same whether the path is absent, sudo wants a password, sudo is not
// installed, or stat is not installed. Reading that as "absent" is a security
// control reporting OK precisely when it cannot tell — and one sudoers change
// would turn every drift check green on a guest where nothing holds.
//
// stat prints one line per operand it could read and sends the rest to stderr,
// so naming a path that must exist answers both questions in a single round
// trip: no line at all means stat never ran, and exactly one line means it ran
// and the path under test was not there.
const statControlPath = "/"

// pathState is what a privileged stat probe managed to establish about a path.
// The zero value is the one that is not an answer about the path at all, so a
// caller that forgets to switch on it fails closed rather than open.
type pathState int

const (
	pathUnprovable pathState = iota
	pathAbsent
	pathPresent
)

// statPath probes path as root, reporting what was established and the file
// type stat gave when the path is there.
func (a *Adapter) statPath(ctx context.Context, rep *MCPBrokerReport, name, path string) (pathState, string, error) {
	res, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%F", statControlPath, path)
	if err != nil {
		return pathUnprovable, "", err
	}

	// Split on lines rather than fields: a file type is words ("regular file",
	// "symbolic link"), and one line is one operand's answer.
	var lines []string
	for _, l := range strings.Split(res.out, "\n") {
		if s := strings.TrimSpace(l); s != "" {
			lines = append(lines, s)
		}
	}

	// The control path is a directory on every guest this runs on. Anything else
	// in that slot means the reply did not come from the command this probe
	// believes it ran, which is not a fact about the path under test.
	if len(lines) == 0 || lines[0] != "directory" {
		return pathUnprovable, "", nil
	}
	switch len(lines) {
	case 1:
		return pathAbsent, "", nil
	case 2:
		return pathPresent, lines[1], nil
	default:
		return pathUnprovable, "", nil
	}
}

// probeUnusable is the failure for a root probe that never ran.
//
// It is recorded as drift rather than as a missing precondition, and the
// difference is deliberate: "not provisioned" is a claim about the guest, and
// nothing about the guest was established. Classifying it would be the same
// guess these probes exist to stop making.
func (a *Adapter) probeUnusable(rep *MCPBrokerReport, name, subject string) *Error {
	return a.brokerFailed(rep, name,
		"could not establish whether "+subject+" exists",
		"this check reads the guest as root; confirm passwordless sudo still works for the operator identity and that `stat` is present")
}

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
	return a.verifyBrokerUserFor(ctx, rep, HermesUser)
}

func (a *Adapter) verifyBrokerUserFor(ctx context.Context, rep *MCPBrokerReport, agentUser string) error {
	const name = "broker_user"
	res, err := a.brokerProbe(ctx, rep, name, "id", "-u", TorioMCPUser)
	if err != nil {
		return err
	}
	uid := res.trimmed()
	if res.exit != 0 || uid == "" {
		return a.brokerMissing(rep, name, "torio-mcp user not found", "run `torio mcp install` to provision the broker identity")
	}

	passwd, err := a.brokerProbe(ctx, rep, name, "getent", "passwd", TorioMCPUser)
	if err != nil {
		return err
	}
	fields := strings.Split(passwd.trimmed(), ":")
	if passwd.exit != 0 || len(fields) != 7 || fields[0] != TorioMCPUser || fields[5] != TorioMCPHome || fields[6] != brokerLoginShell {
		return a.brokerFailed(rep, name, "passwd entry does not match the broker identity contract",
			"restore torio-mcp with home /home/torio-mcp and shell /usr/sbin/nologin")
	}
	numericUID, uidErr := strconv.Atoi(fields[2])
	numericGID, gidErr := strconv.Atoi(fields[3])
	if uidErr != nil || gidErr != nil || fields[2] != uid || numericUID <= 0 || numericGID <= 0 {
		return a.brokerFailed(rep, name, "broker uid or primary gid is privileged or inconsistent",
			"restore torio-mcp as a dedicated non-root system identity")
	}

	primary, err := a.brokerProbe(ctx, rep, name, "id", "-gn", TorioMCPUser)
	if err != nil {
		return err
	}
	if primary.exit != 0 || primary.trimmed() != TorioMCPUser {
		return a.brokerFailed(rep, name, "primary group is not torio-mcp", "restore the broker's dedicated primary group")
	}

	groups, err := a.brokerProbe(ctx, rep, name, "id", "-nG", TorioMCPUser)
	if err != nil {
		return err
	}
	if groups.exit != 0 {
		return a.brokerFailed(rep, name, "cannot read broker group membership", "inspect the torio-mcp identity on the guest")
	}
	seenClient := false
	for _, group := range strings.Fields(groups.out) {
		switch group {
		case TorioMCPUser:
		case TorioMCPClientsGroup:
			seenClient = true
		default:
			return a.brokerFailed(rep, name, "broker has an unexpected supplementary group",
				"remove every torio-mcp membership except torio-mcp and torio-mcp-clients")
		}
	}
	if !seenClient {
		return a.brokerFailed(rep, name, "broker is not in torio-mcp-clients",
			"add only torio-mcp-clients so the broker can publish its socket")
	}

	sudo, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "-l", "-U", TorioMCPUser)
	if err != nil {
		return err
	}
	if sudo.exit == 0 {
		return a.brokerFailed(rep, name, "broker has sudo authority", "remove every sudoers grant for torio-mcp")
	}
	if sudo.exit != 1 {
		return a.brokerFailed(rep, name, "could not prove the absence of sudo authority", "inspect sudoers and retry")
	}
	agentUID, err := a.brokerProbe(ctx, rep, name, "id", "-u", agentUser)
	if err != nil {
		return err
	}
	if agentUID.exit != 0 || agentUID.trimmed() == "" || agentUID.trimmed() == uid {
		return a.brokerFailed(rep, name, "broker identity does not have a uid distinct from the agent",
			"restore torio-mcp and the selected backend as separate non-root identities")
	}

	rep.record(name, true, "uid="+uid+" dedicated unprivileged identity")
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

func (a *Adapter) verifyAgentIsBrokerClient(ctx context.Context, rep *MCPBrokerReport, agentUser string) error {
	name := agentUser + "_broker_client"
	res, err := a.brokerProbe(ctx, rep, name, "id", "-nG", agentUser)
	if err != nil {
		return err
	}
	if res.exit != 0 {
		return a.brokerFailed(rep, name, "cannot read agent group membership", "confirm the selected backend user exists on the guest")
	}
	if !hasGroup(res.out, TorioMCPClientsGroup) {
		return a.brokerFailed(rep, name, "agent is not in torio-mcp-clients", "run `torio mcp install`; without this group the agent cannot reach the broker socket")
	}
	rep.record(name, true, "member")
	return nil
}

// verifyHermesNotBrokerOwner is the custody invariant. It rejects direct owner
// membership and indirect privilege escalation: sudo or any group outside the
// managed guest set could bypass the broker home's 0700 mode. It is checked
// separately from client-group membership so a broken security boundary is not
// confused with missing socket plumbing.
func (a *Adapter) verifyHermesNotBrokerOwner(ctx context.Context, rep *MCPBrokerReport) error {
	return a.verifyAgentNotBrokerOwner(ctx, rep, HermesUser)
}

func (a *Adapter) verifyAgentNotBrokerOwner(ctx context.Context, rep *MCPBrokerReport, agentUser string) error {
	name := agentUser + "_not_broker_owner"
	res, err := a.brokerProbe(ctx, rep, name, "id", "-nG", agentUser)
	if err != nil {
		return err
	}
	if res.exit != 0 {
		return a.brokerFailed(rep, name, "cannot read agent group membership", "confirm the selected backend user exists on the guest")
	}
	seen := map[string]bool{}
	for _, group := range strings.Fields(res.out) {
		switch group {
		case agentUser, TorioProjectsGroup, TorioMCPClientsGroup:
			seen[group] = true
		case TorioMCPUser:
			return a.brokerFailed(rep, name, "agent is in the torio-mcp group",
				"remove the selected backend user from torio-mcp; membership makes every broker credential readable by the agent identity (ADR-0004)")
		default:
			return a.brokerFailed(rep, name, "agent has an unexpected supplementary group",
				"remove groups outside its primary group, torio-projects, and torio-mcp-clients; privileged groups bypass credential custody")
		}
	}
	for _, required := range []string{agentUser, TorioProjectsGroup, TorioMCPClientsGroup} {
		if !seen[required] {
			return a.brokerFailed(rep, name, "agent group membership does not match the managed guest contract",
				"restore the selected backend's primary, torio-projects, and torio-mcp-clients groups only")
		}
	}
	sudo, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "-l", "-U", agentUser)
	if err != nil {
		return err
	}
	if sudo.exit == 0 {
		return a.brokerFailed(rep, name, "agent has sudo authority", "remove every sudoers grant for the selected backend user")
	}
	if sudo.exit != 1 {
		return a.brokerFailed(rep, name, "could not prove the absence of agent sudo authority", "inspect sudoers and retry")
	}
	rep.record(name, true, "managed groups only; no sudo authority")
	return nil
}

func (a *Adapter) verifyBrokerHome(ctx context.Context, rep *MCPBrokerReport) error {
	name := "path:" + torioMCPHomeSpec.Path

	st, kind, err := a.statPath(ctx, rep, name, torioMCPHomeSpec.Path)
	if err != nil {
		return err
	}
	if st == pathUnprovable {
		return a.probeUnusable(rep, name, "the broker credential store")
	}
	if st == pathAbsent || kind != "directory" {
		return a.brokerMissing(rep, name, "not a directory", "run `torio mcp install` to provision the broker credential store")
	}

	og, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%U:%G %a", torioMCPHomeSpec.Path)
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
	if owner != torioMCPHomeSpec.Owner || group != torioMCPHomeSpec.Group {
		return a.brokerFailed(rep, name,
			fmt.Sprintf("owner:group %s:%s, want %s:%s", owner, group, torioMCPHomeSpec.Owner, torioMCPHomeSpec.Group),
			"fix broker home ownership on the guest")
	}
	if !modeMatches(torioMCPHomeSpec, mode) {
		return a.brokerFailed(rep, name,
			fmt.Sprintf("mode %s, want one of %v", mode, torioMCPHomeSpec.Modes),
			"the broker credential store must not be readable outside torio-mcp (ADR-0004)")
	}
	rep.record(name, true, fmt.Sprintf("%s:%s %s", owner, group, mode))
	return nil
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

func (a *Adapter) verifyMCPBrokerUnit(ctx context.Context, rep *MCPBrokerReport) error {
	const name = "broker_unit"
	metadata, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%F %U:%G %a", "/etc/systemd/system", TorioMCPBrokerUnitPath)
	if err != nil {
		return err
	}
	lines := nonEmptyLines(metadata.out)
	if len(lines) == 0 || lines[0] != "directory root:root 755" {
		return a.brokerFailed(rep, name, "system unit directory is not trusted", "restore /etc/systemd/system to root:root 0755")
	}
	if len(lines) == 1 {
		return a.brokerFailed(rep, name, "broker runtime exists without the trusted system unit",
			"stop the unauthorized runtime, remove its sockets, and run `torio mcp install`")
	}
	if len(lines) != 2 || lines[1] != "regular file root:root 644" {
		return a.brokerFailed(rep, name, "broker system unit ownership or mode drift", "reinstall the broker unit")
	}

	enabled, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "systemctl", "is-enabled", TorioMCPBrokerUnitName)
	if err != nil {
		return err
	}
	if enabled.exit != 0 || enabled.trimmed() != "enabled" {
		return a.brokerFailed(rep, name, "broker system unit is not enabled", "run `torio mcp install` on the host")
	}
	active, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "systemctl", "is-active", TorioMCPBrokerUnitName)
	if err != nil {
		return err
	}
	if active.exit != 0 || active.trimmed() != "active" {
		return a.brokerFailed(rep, name, "broker system unit is not active", "inspect service logs, then run `torio mcp install` on the host")
	}
	content, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "cat", TorioMCPBrokerUnitPath)
	if err != nil {
		return err
	}
	if content.exit != 0 || content.out != string(mcpBrokerUnit()) {
		return a.brokerFailed(rep, name, "broker system unit content drift", "run `torio mcp install` on the host")
	}
	effective, err := a.brokerProbe(ctx, rep, name, mcpBrokerEffectiveUnitShowArgs()...)
	if err != nil {
		return err
	}
	if effective.exit != 0 || !effectiveMCPBrokerUnitExact(effective.out) {
		return a.brokerFailed(rep, name, "effective broker system unit drift", "remove systemd drop-ins or runtime overrides, then run `torio mcp install` on the host")
	}
	rep.record(name, true, "enabled and active")
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

	st, kind, err := a.statPath(ctx, rep, name, HermesMCPTokensPath)
	if err != nil {
		return err
	}
	if st == pathUnprovable {
		return a.probeUnusable(rep, name, "the Hermes MCP token store")
	}
	if st == pathAbsent {
		rep.record(name, true, "absent")
		return nil
	}
	if kind != "directory" {
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
