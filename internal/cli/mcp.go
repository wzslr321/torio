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
		Short: "Inspect the MCP credential broker boundary",
		Long: "MCP servers are reached through a broker running under its own guest identity " +
			"(" + lima.TorioMCPUser + "), so no upstream credential exists under the identity the " +
			"agent has a shell as. Torio never handles those credentials itself: it provisions the " +
			"boundary and proves it holds (ADR-0022).",
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
	BrokerUser   string         `json:"broker_user"`
	BrokerHome   string         `json:"broker_home"`
	ClientsGroup string         `json:"clients_group"`
	AgentUser    string         `json:"agent_user"`
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
	_, err := fmt.Fprintf(a.stdout,
		"\nBroker boundary holds on %s.\n"+
			"Credential owner:   %s (home %s, readable by nobody else)\n"+
			"Agent identity:     %s — may open the broker socket, cannot read its credentials\n"+
			"Client group:       %s\n",
		rep.Instance, lima.TorioMCPUser, lima.TorioMCPHome, lima.HermesUser, lima.TorioMCPClientsGroup)
	return err
}

func newMCPInstallCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Provision the broker identity and its credential store",
		Long: "Create the unprivileged " + lima.TorioMCPUser + " identity, its 0700 credential store, " +
			"the " + lima.TorioMCPClientsGroup + " group, and the root-owned policy directory; then prove " +
			"the result instead of trusting the exit codes that produced it. Idempotent: a re-run that " +
			"changes nothing reports changed:false. Grants nothing beyond the client-group membership " +
			"hermes needs to open the broker socket, and accepts no secrets.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			rep, err := a.newLima().InstallMCPBroker(ctx)
			if err != nil {
				ce := mapLimaError("mcp.install", err)
				ce.Details = mcpInstallDetails(rep)
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
	return map[string]any{"instance": rep.Instance, "checks": checks}
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
