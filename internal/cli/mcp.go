package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/lima"
)

// newMCPCmd builds the `torio mcp` parent (ADR-0004). Like `torio vm` and
// `torio serve`, the parent takes no action itself: an absent or unknown
// subcommand is a usage error, fail-closed.
func newMCPCmd(a *app) *cobra.Command {
	m := &cobra.Command{
		Use:   "mcp",
		Short: "Provision and inspect the MCP credential boundary",
		Long: "Install and inspect the dedicated " + lima.TorioMCPUser + " credential broker. " +
			"`install` deploys the broker and relay and wires the selected backend; `login` performs " +
			"interactive OAuth as the broker identity; `status` proves custody, policy, unit, and sockets. " +
			"Torio accepts no MCP credential as an argument or host file (ADR-0004).",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError("no subcommand given; run 'torio mcp --help'")
			}
			return usageError(fmt.Sprintf("unknown mcp subcommand %q", args[0]))
		},
	}
	m.AddCommand(newMCPInstallCmd(a))
	m.AddCommand(newMCPLoginCmd(a))
	m.AddCommand(newMCPStatusCmd(a))
	return m
}

func newMCPStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Prove the broker's custody boundary on the guest",
		Long: "Verify that the broker identity exists, that its credential store is readable by " +
			"nobody else, that the selected backend may reach the broker socket but is not a member of the " +
			"broker's own group, that its MCP declaration matches policy, and—when OAuth is complete—that " +
			"the unit and exact policy sockets are live. Proves and reports; repairs nothing. A guest that was never provisioned is " +
			"an unmet precondition (exit 3); a boundary that no longer holds is drift (exit 6).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			rep, err := a.verifyMCP(ctx, a.newLima(), a.backend.Identity())
			if err != nil {
				ce := mapLimaError("mcp.status", err)
				// Surface the checks recorded up to the failure (already bounded and
				// redacted) so the operator sees exactly which boundary did not hold.
				ce.Details = mcpReportDetails(rep)
				return ce
			}
			return a.emitMCPStatus(rep)
		},
	}
}

// mcpStatusData is the `data` object of a successful `mcp status`. The identity
// and path fields are constants rather than probe output: they tell the operator
// which boundary was proven, and they are the same values a reader of ADR-0004
// will be looking for.
type mcpStatusData struct {
	Instance     string        `json:"instance"`
	Checks       []checkData   `json:"checks"`
	Policy       mcpPolicyData `json:"policy"`
	BrokerUser   string        `json:"broker_user"`
	BrokerHome   string        `json:"broker_home"`
	ClientsGroup string        `json:"clients_group"`
	AgentUser    string        `json:"agent_user"`
}

// mcpPolicyData is the grant, in the envelope. The checks already say the policy
// parsed; this says what it grants, in a form a caller can act on. ADR-0004
// makes that legibility the point of the arrangement, and a count recovered by
// parsing an English detail line is not legible to anything but a human.
type mcpPolicyData struct {
	// Digest is the generation identifier a running broker publishes, so a caller
	// can tell two reports of one grant from reports of two grants.
	Digest   string                 `json:"digest"`
	Services []mcpPolicyServiceData `json:"services"`
}

// mcpPolicyServiceData is one service's grant. Unlike checkData, these fields
// are values from the policy documents rather than derived markers, and they are
// safe to carry for a specific reason: a service name has passed
// lima.ValidateServiceName and an endpoint the policy schema's endpoint
// rule, so neither can hold a control byte, a path traversal, or an embedded
// credential. A guest filename, of which none of that is true, is still never
// emitted anywhere in this file.
type mcpPolicyServiceData struct {
	Name             string `json:"name"`
	UpstreamEndpoint string `json:"upstream_endpoint"`
	Tools            int    `json:"tools"`
	WriteTools       int    `json:"write_tools"`
}

func mcpPolicyPayload(g lima.PolicyGrant) mcpPolicyData {
	services := make([]mcpPolicyServiceData, 0, len(g.Services))
	for _, s := range g.Services {
		services = append(services, mcpPolicyServiceData{
			Name:             s.Name,
			UpstreamEndpoint: s.UpstreamEndpoint,
			Tools:            s.Tools,
			WriteTools:       s.WriteTools,
		})
	}
	return mcpPolicyData{Digest: g.Digest, Services: services}
}

// unknownAgentUser stands in when a report failed before it could establish the
// backend's guest identity. Naming a backend here instead would assert an
// identity Torio never read.
const unknownAgentUser = "unknown"

func mcpStatusPayload(rep lima.MCPBrokerReport) mcpStatusData {
	agentUser := rep.AgentUser
	if agentUser == "" {
		agentUser = unknownAgentUser
	}
	return mcpStatusData{
		Instance:     rep.Instance,
		Checks:       checkPayload(rep.Checks),
		Policy:       mcpPolicyPayload(rep.Policy),
		BrokerUser:   lima.TorioMCPUser,
		BrokerHome:   lima.TorioMCPHome,
		ClientsGroup: lima.TorioMCPClientsGroup,
		AgentUser:    agentUser,
	}
}

// mcpReportDetails renders the checks recorded before a failure as error
// details, so a failing status still names the boundary that did not hold.
// Values pass through the final redactor in fail().
func mcpReportDetails(rep lima.MCPBrokerReport) map[string]any {
	if len(rep.Checks) == 0 {
		return nil
	}
	return map[string]any{"instance": rep.Instance, "checks": checkDetails(rep.Checks)}
}

// emitMCPStatus renders a proven boundary. JSON mode emits exactly one success
// envelope; human mode prints one line per check plus the identity separation
// the checks establish, because "ok" on its own does not tell an operator what
// was actually guaranteed.
func (a *app) emitMCPStatus(rep lima.MCPBrokerReport) error {
	agentUser := rep.AgentUser
	if agentUser == "" {
		agentUser = unknownAgentUser
	}
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("mcp.status", mcpStatusPayload(rep)))
	}
	if err := a.writeCheckLines(rep.Checks); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(a.stdout,
		"\nBroker boundary holds on %s.\n"+
			"Credential owner:   %s (home %s, readable by nobody else)\n"+
			"Agent identity:     %s — may open the broker socket, cannot read its credentials\n"+
			"Client group:       %s\n",
		rep.Instance, lima.TorioMCPUser, lima.TorioMCPHome, agentUser, lima.TorioMCPClientsGroup); err != nil {
		return err
	}
	return a.writeMCPPolicy(rep.Policy)
}

// writeMCPPolicy prints the grant the policy documents carry. The check line
// above it already gives the totals; what this adds is which service holds them
// and where each one's traffic goes — the half of "what is granted" that a
// number cannot answer.
//
// The values are printed as parsed, which is safe for the reason given on
// mcpPolicyServiceData: both fields are schema-validated before they reach here.
func (a *app) writeMCPPolicy(g lima.PolicyGrant) error {
	if len(g.Services) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(a.stdout, "\nGranted policy (generation %s):\n", g.Digest); err != nil {
		return err
	}
	width := 0
	for _, s := range g.Services {
		if len(s.Name) > width {
			width = len(s.Name)
		}
	}
	for _, s := range g.Services {
		if _, err := fmt.Fprintf(a.stdout, "  %-*s  %d tool(s), %d write  ->  %s\n",
			width, s.Name, s.Tools, s.WriteTools, s.UpstreamEndpoint); err != nil {
			return err
		}
	}
	return nil
}

func newMCPInstallCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Provision and verify the MCP credential boundary",
		Long: "Create the unprivileged " + lima.TorioMCPUser + " identity, its 0700 credential store, " +
			"the " + lima.TorioMCPClientsGroup + " group, and the root-owned policy directory; install the " +
			"broker, relay, and systemd unit shipped with this release; then configure the selected backend " +
			"to use only the relay. The unit remains stopped until `torio mcp login <service>` completes OAuth. " +
			"Idempotent: a re-run that changes nothing " +
			"reports changed:false. Accepts no secrets.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			adapter := a.newLima()
			rep, err := a.installMCP(ctx, adapter, a.backend.Identity())
			if err != nil {
				ce := mapLimaError("mcp.install", err)
				ce.Details = mcpInstallDetails(rep)
				if rep.Changed {
					ce.Message += "; guest was partially changed; re-run `torio mcp install` after fixing the reported precondition"
				}
				if rep.RestartRequired {
					ce.Message += "; end the current agent session and open a new one before expecting the backend to use its new client-group membership"
				}
				return ce
			}
			return a.emitMCPInstall(rep)
		},
	}
}

func defaultInstallMCP(ctx context.Context, adapter *lima.Adapter, identity backend.Identity) (lima.MCPBrokerInstallReport, error) {
	executable, err := os.Executable()
	if err != nil {
		return lima.MCPBrokerInstallReport{}, fmt.Errorf("resolve the torio release directory: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	return adapter.ProvisionMCPBrokerFor(ctx, identity, filepath.Dir(executable))
}

func newMCPLoginCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "login <service>",
		Short: "Authorize one policy service as the broker identity",
		Long: "Open a loopback-only OAuth callback tunnel and run the broker's login flow as " +
			lima.TorioMCPUser + ". Torio does not receive or print the token. No SSH agent is forwarded. " +
			"When every policy service has a private OAuth session, the broker unit is enabled and started.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.jsonOut {
				return usageError("mcp login is interactive; --json is not supported")
			}
			service := args[0]
			spec, err := a.newMCPLoginSpec(service)
			if err != nil {
				return mapLimaError("mcp.login", err)
			}
			if err := a.newInteractive().RunInteractive(cmd.Context(), spec); err != nil {
				return mapInteractiveSessionError("mcp.login", "MCP login session", err)
			}
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			activation, err := a.activateMCP(ctx, a.newLima(), a.backend.Identity())
			if err != nil {
				return mapLimaError("mcp.login", err)
			}
			if activation.Activated {
				_, err = fmt.Fprintf(a.stdout, "%s: OAuth session stored by %s; broker is active.\n", service, lima.TorioMCPUser)
				return err
			}
			_, err = fmt.Fprintf(a.stdout, "%s: OAuth session stored by %s; %d policy service(s) still require login before the broker starts.\n",
				service, lima.TorioMCPUser, activation.Pending)
			return err
		},
	}
}

// mcpInstallData is the `data` object of a successful `mcp install`.
type mcpInstallData struct {
	Instance        string        `json:"instance"`
	Changed         bool          `json:"changed"`
	RestartRequired bool          `json:"restart_required"`
	Checks          []checkData   `json:"checks"`
	Policy          mcpPolicyData `json:"policy"`
	BrokerUser      string        `json:"broker_user"`
	BrokerHome      string        `json:"broker_home"`
	ClientsGroup    string        `json:"clients_group"`
	PolicyDir       string        `json:"policy_dir"`
	AgentUser       string        `json:"agent_user"`
}

func mcpInstallPayload(rep lima.MCPBrokerInstallReport) mcpInstallData {
	agentUser := rep.AgentUser
	if agentUser == "" {
		agentUser = unknownAgentUser
	}
	return mcpInstallData{
		Instance:        rep.Instance,
		Changed:         rep.Changed,
		RestartRequired: rep.RestartRequired,
		Checks:          checkPayload(rep.Checks),
		Policy:          mcpPolicyPayload(rep.Policy),
		BrokerUser:      lima.TorioMCPUser,
		BrokerHome:      lima.TorioMCPHome,
		ClientsGroup:    lima.TorioMCPClientsGroup,
		PolicyDir:       lima.TorioMCPPolicyDir,
		AgentUser:       agentUser,
	}
}

func mcpInstallDetails(rep lima.MCPBrokerInstallReport) map[string]any {
	if len(rep.Checks) == 0 {
		return nil
	}
	return map[string]any{
		"instance":         rep.Instance,
		"changed":          rep.Changed,
		"restart_required": rep.RestartRequired,
		"checks":           checkDetails(rep.Checks),
	}
}

// emitMCPInstall renders a completed provisioning. The restart line is not a
// nicety: a long-running process does not gain a group because the group
// database changed under it, so without it the operator gets a broker their
// agent silently cannot reach.
func (a *app) emitMCPInstall(rep lima.MCPBrokerInstallReport) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("mcp.install", mcpInstallPayload(rep)))
	}
	if err := a.writeCheckLines(rep.Checks); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(a.stdout,
		"\nBroker boundary provisioned on %s (changed: %t).\n"+
			"Credential owner:   %s (home %s, mode 0700)\n"+
			"Policy directory:   %s (root-owned, world-readable: the grant is legible, the credentials are not)\n",
		rep.Instance, rep.Changed, lima.TorioMCPUser, lima.TorioMCPHome, lima.TorioMCPPolicyDir); err != nil {
		return err
	}
	// Before the restart note, not after: the restart is the thing left to do, so
	// it stays the last line an operator reads.
	if err := a.writeMCPPolicy(rep.Policy); err != nil {
		return err
	}
	if rep.RestartRequired {
		agentUser := rep.AgentUser
		if agentUser == "" {
			agentUser = unknownAgentUser
		}
		if _, err := fmt.Fprintf(a.stdout,
			"\n%s only just joined %s. The running backend keeps the groups it started with,\n"+
				"so end the current agent session and open a new one before expecting the agent to reach the broker.\n",
			agentUser, lima.TorioMCPClientsGroup); err != nil {
			return err
		}
	}
	return nil
}
