package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/lima"
)

// newMCPCmd builds the `torio mcp` parent (ADR-0022). Like `torio vm` and
// `torio serve`, the parent takes no action itself: an absent or unknown
// subcommand is a usage error, fail-closed.
func newMCPCmd(a *app) *cobra.Command {
	m := &cobra.Command{
		Use:   "mcp",
		Short: "Provision and inspect the MCP credential boundary",
		Long: "Provision the dedicated " + lima.TorioMCPUser + " identity, client group, private " +
			"credential store, and root-owned policy boundary. The broker daemon is not installed or " +
			"activated until its OAuth and upstream transport have an accepted contract. Torio accepts " +
			"no MCP credentials through this CLI (ADR-0027).",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError("no subcommand given; run 'torio mcp --help'")
			}
			return usageError(fmt.Sprintf("unknown mcp subcommand %q", args[0]))
		},
	}
	m.AddCommand(newMCPInstallCmd(a))
	m.AddCommand(newMCPStatusCmd(a))
	return m
}

func newMCPStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Prove the broker's custody boundary on the guest",
		Long: "Verify that the broker identity exists, that its credential store is readable by " +
			"nobody else, that hermes may reach the broker socket but is not a member of the " +
			"broker's own group, and that no MCP credential has reappeared under the Hermes " +
			"profile. Proves and reports; repairs nothing. A guest that was never provisioned is " +
			"an unmet precondition (exit 3); a boundary that no longer holds is drift (exit 6).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			rep, err := a.newLima().VerifyMCPBroker(ctx)
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
// which boundary was proven, and they are the same values a reader of ADR-0022
// will be looking for.
type mcpStatusData struct {
	Instance     string         `json:"instance"`
	Checks       []mcpCheckData `json:"checks"`
	Policy       mcpPolicyData  `json:"policy"`
	BrokerUser   string         `json:"broker_user"`
	BrokerHome   string         `json:"broker_home"`
	ClientsGroup string         `json:"clients_group"`
	AgentUser    string         `json:"agent_user"`
}

// mcpPolicyData is the grant, in the envelope. The checks already say the policy
// parsed; this says what it grants, in a form a caller can act on. ADR-0022
// makes that legibility the point of the arrangement, and a count recovered by
// parsing an English detail line is not legible to anything but a human.
type mcpPolicyData struct {
	// Digest is the generation identifier a running broker publishes, so a caller
	// can tell two reports of one grant from reports of two grants.
	Digest   string                 `json:"digest"`
	Services []mcpPolicyServiceData `json:"services"`
}

// mcpPolicyServiceData is one service's grant. Unlike mcpCheckData, these fields
// are values from the policy documents rather than derived markers, and they are
// safe to carry for a specific reason: a service name has passed
// mcpbroker.ValidateServiceName and an endpoint the policy schema's endpoint
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

// mcpCheckData is one proven boundary in the envelope. Detail is a short derived
// value — a uid, a mode, a count — never a raw output blob and never a guest
// filename.
type mcpCheckData struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func mcpStatusPayload(rep lima.MCPBrokerReport) mcpStatusData {
	checks := make([]mcpCheckData, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		checks = append(checks, mcpCheckData{Name: c.Name, OK: c.OK, Detail: c.Detail})
	}
	return mcpStatusData{
		Instance:     rep.Instance,
		Checks:       checks,
		Policy:       mcpPolicyPayload(rep.Policy),
		BrokerUser:   lima.TorioMCPUser,
		BrokerHome:   lima.TorioMCPHome,
		ClientsGroup: lima.TorioMCPClientsGroup,
		AgentUser:    lima.HermesUser,
	}
}

// mcpReportDetails renders the checks recorded before a failure as error
// details, so a failing status still names the boundary that did not hold.
// Values pass through the final redactor in fail().
func mcpReportDetails(rep lima.MCPBrokerReport) map[string]any {
	if len(rep.Checks) == 0 {
		return nil
	}
	checks := make([]map[string]any, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		checks = append(checks, map[string]any{"name": c.Name, "ok": c.OK, "detail": c.Detail})
	}
	return map[string]any{"instance": rep.Instance, "checks": checks}
}

// emitMCPStatus renders a proven boundary. JSON mode emits exactly one success
// envelope; human mode prints one line per check plus the identity separation
// the checks establish, because "ok" on its own does not tell an operator what
// was actually guaranteed.
func (a *app) emitMCPStatus(rep lima.MCPBrokerReport) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("mcp.status", mcpStatusPayload(rep)))
	}
	for _, c := range rep.Checks {
		mark := "ok"
		if !c.OK {
			mark = "FAIL"
		}
		if _, err := fmt.Fprintf(a.stdout, "[%s] %s: %s\n", mark, c.Name, c.Detail); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(a.stdout,
		"\nBroker boundary holds on %s.\n"+
			"Credential owner:   %s (home %s, readable by nobody else)\n"+
			"Agent identity:     %s — may open the broker socket, cannot read its credentials\n"+
			"Client group:       %s\n",
		rep.Instance, lima.TorioMCPUser, lima.TorioMCPHome, lima.HermesUser, lima.TorioMCPClientsGroup); err != nil {
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
			"the " + lima.TorioMCPClientsGroup + " group, and the root-owned policy directory, then prove " +
			"the identity and policy boundaries. The daemon is not installed or activated until its OAuth " +
			"and upstream transport have an accepted contract. Idempotent: a re-run that changes nothing " +
			"reports changed:false. Accepts no secrets.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			rep, err := a.newLima().ProvisionMCPBroker(ctx)
			if err != nil {
				ce := mapLimaError("mcp.install", err)
				ce.Details = mcpInstallDetails(rep)
				if rep.Changed {
					ce.Message += "; guest was partially changed; the MCP daemon was not installed or activated"
				}
				if rep.RestartRequired {
					ce.Message += "; run `torio serve restart` before expecting the backend to use its new client-group membership"
				}
				return ce
			}
			return a.emitMCPInstall(rep)
		},
	}
}

// mcpInstallData is the `data` object of a successful `mcp install`.
type mcpInstallData struct {
	Instance        string         `json:"instance"`
	Changed         bool           `json:"changed"`
	RestartRequired bool           `json:"restart_required"`
	Checks          []mcpCheckData `json:"checks"`
	Policy          mcpPolicyData  `json:"policy"`
	BrokerUser      string         `json:"broker_user"`
	BrokerHome      string         `json:"broker_home"`
	ClientsGroup    string         `json:"clients_group"`
	PolicyDir       string         `json:"policy_dir"`
}

func mcpInstallPayload(rep lima.MCPBrokerInstallReport) mcpInstallData {
	checks := make([]mcpCheckData, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		checks = append(checks, mcpCheckData{Name: c.Name, OK: c.OK, Detail: c.Detail})
	}
	return mcpInstallData{
		Instance:        rep.Instance,
		Changed:         rep.Changed,
		RestartRequired: rep.RestartRequired,
		Checks:          checks,
		Policy:          mcpPolicyPayload(rep.Policy),
		BrokerUser:      lima.TorioMCPUser,
		BrokerHome:      lima.TorioMCPHome,
		ClientsGroup:    lima.TorioMCPClientsGroup,
		PolicyDir:       lima.TorioMCPPolicyDir,
	}
}

func mcpInstallDetails(rep lima.MCPBrokerInstallReport) map[string]any {
	if len(rep.Checks) == 0 {
		return nil
	}
	checks := make([]map[string]any, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		checks = append(checks, map[string]any{"name": c.Name, "ok": c.OK, "detail": c.Detail})
	}
	return map[string]any{
		"instance":         rep.Instance,
		"changed":          rep.Changed,
		"restart_required": rep.RestartRequired,
		"checks":           checks,
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
	for _, c := range rep.Checks {
		mark := "ok"
		if !c.OK {
			mark = "FAIL"
		}
		if _, err := fmt.Fprintf(a.stdout, "[%s] %s: %s\n", mark, c.Name, c.Detail); err != nil {
			return err
		}
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
		if _, err := fmt.Fprintf(a.stdout,
			"\n%s only just joined %s. The running backend keeps the groups it started with,\n"+
				"so restart it before expecting the agent to reach the broker:  torio serve restart\n",
			lima.HermesUser, lima.TorioMCPClientsGroup); err != nil {
			return err
		}
	}
	return nil
}
