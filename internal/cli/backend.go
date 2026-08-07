package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/lima"
)

func newBackendCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backend",
		Short: "Inspect and authenticate the agent backend this instance runs",
		Long: "An instance runs one agent backend, chosen at `vm init`. These commands " +
			"report what it is and, for a backend that holds a credential of its own, " +
			"open the session where an operator grants it one.\n\n" +
			"Torio never stores, forwards or reads a backend credential. `login` only " +
			"opens a terminal on the guest, as the backend identity; everything after " +
			"that is between the operator and whoever issues the grant.",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError("no subcommand given; run 'torio backend --help'")
			}
			return usageError(fmt.Sprintf("unknown backend subcommand %q", args[0]))
		},
	}
	cmd.AddCommand(newBackendStatusCmd(a))
	cmd.AddCommand(newBackendLoginCmd(a))
	return cmd
}

func newBackendStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report the backend, its version, and whether it holds a credential",
		Long: "Report what this instance runs and what it declares: the backend name, " +
			"the version installed on the guest, whether a credential is present, and " +
			"which capabilities — a project registry, a guest service, an interactive " +
			"session — the backend has.\n\n" +
			"It reads the guest and changes nothing, and it never reaches the network: " +
			"whether a credential is still valid is between the operator and whoever " +
			"issued it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			opUser, err := a.lookupOperatorUser()
			if err != nil {
				return &CLIError{Exit: ExitExternal, Code: "OPERATOR_LOOKUP_FAILED", Command: "backend.status", Message: err.Error()}
			}
			rep, err := a.newLima().Bootstrap(ctx, lima.BootstrapOptions{OperatorUser: opUser, Backend: a.backend})
			if err != nil {
				ce := mapLimaError("backend.status", err)
				ce.Details = bootstrapReportDetails(rep)
				return ce
			}
			return a.emitBackendStatus(rep)
		},
	}
}

// backendStatusData is the `data` object for `backend status`.
type backendStatusData struct {
	Backend string `json:"backend"`
	User    string `json:"user"`
	Version string `json:"version"`
	// Credentials is "present", "absent" or "not-applicable" — the last for a
	// backend Torio has no offline way to ask, which is a different answer from
	// "absent" and must not be rendered as one.
	Credentials string `json:"credentials"`
	// The capabilities the backend declares. They are reported even when false,
	// because "this backend has no service" is the answer to a question
	// operators keep asking, and an omitted key answers nothing.
	RegistryDeclared bool `json:"registry_declared"`
	ServiceDeclared  bool `json:"service_declared"`
	SessionDeclared  bool `json:"session_declared"`
	// MCPServers names the MCP servers the guest is configured with, if the
	// backend can report them. They are names only, read from a file the agent
	// owns: this is what is configured, never what is permitted.
	MCPServers string `json:"mcp_servers,omitempty"`
}

func (a *app) emitBackendStatus(rep lima.BootstrapReport) error {
	id := a.backend.Identity()
	data := backendStatusData{
		Backend:          id.Name,
		User:             id.GuestUser,
		Version:          checkDetail(rep, id.Name+"_version"),
		Credentials:      credentialState(checkDetail(rep, id.Name+"_auth")),
		RegistryDeclared: a.backend.Registry() != nil,
		ServiceDeclared:  a.backend.Service() != nil,
		SessionDeclared:  a.backend.Session() != nil,
		MCPServers:       checkDetail(rep, id.Name+"_mcp_servers"),
	}
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("backend.status", data))
	}
	if _, err := fmt.Fprintf(a.stdout,
		"Backend %s (guest user %s)\n"+
			"  version:     %s\n"+
			"  credential:  %s\n"+
			"  declares:    registry=%t service=%t session=%t\n",
		data.Backend, data.User, orNone(data.Version), data.Credentials,
		data.RegistryDeclared, data.ServiceDeclared, data.SessionDeclared); err != nil {
		return err
	}
	if data.MCPServers != "" {
		_, err := fmt.Fprintf(a.stdout, "  mcp servers: %s\n", data.MCPServers)
		return err
	}
	return nil
}

// checkDetail returns the recorded detail of one bootstrap check, empty when
// the backend recorded no such check. An absent check is not a failure here:
// a backend records only what it actually has.
func checkDetail(rep lima.BootstrapReport, name string) string {
	for _, c := range rep.Checks {
		if c.Name == name {
			return c.Detail
		}
	}
	return ""
}

// credentialState renders the auth probe's detail as one of three answers.
// "not-applicable" is deliberately distinct from "absent": a backend Torio has
// no offline way to ask about has not been found to be logged out.
func credentialState(detail string) string {
	switch {
	case detail == "":
		return "not-applicable"
	case len(detail) >= 7 && detail[:7] == "credent" && contains(detail, "present"):
		return "present"
	default:
		return "absent"
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func orNone(s string) string {
	if s == "" {
		return "(not reported)"
	}
	return s
}

func newBackendLoginCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Open a guest terminal as the backend identity so it can be granted a credential",
		Long: "Open an interactive terminal on the guest, as the backend's own identity, " +
			"and start the backend there so its login flow runs. No SSH agent is " +
			"forwarded and the session carries no Git remote write capability.\n\n" +
			"The grant belongs to the box: it is issued to this guest identity and can " +
			"be revoked without touching the operator's own. Torio never copies a " +
			"credential in from the host — a shared identity would couple revocation to " +
			"a machine the operator also works on, and make the box's activity " +
			"indistinguishable from their own.\n\n" +
			"Torio sees none of it. It builds the transport and hands over the terminal.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.jsonOut {
				return usageError("backend login is interactive; --json is not supported")
			}
			session := a.backend.Session()
			if session == nil {
				return &CLIError{
					Exit:    ExitPrecondition,
					Code:    "BACKEND_NO_SESSION",
					Command: "backend.login",
					Message: fmt.Sprintf("backend %q declares no interactive session, so there is no terminal to log in from", a.backend.Identity().Name),
				}
			}
			spec, err := lima.BackendLoginSpec(session.LoginArgv)
			if err != nil {
				return mapLimaError("backend.login", err)
			}
			// The session is bound by the command's own context, not by the
			// operation timeout: a login flow waits on a human pasting a code
			// from a browser, and killing it on a 30-second budget would be
			// Torio deciding how fast someone can read.
			runErr := a.newInteractive().RunInteractive(cmd.Context(), spec)
			if _, err := fmt.Fprintf(a.stdout,
				"%s: login session ended. Run `torio backend status` to see whether a credential is now present.\n",
				a.backend.Identity().Name); err != nil {
				return err
			}
			if runErr != nil {
				return mapInteractiveSessionError("backend.login", "login session", runErr)
			}
			return nil
		},
	}
}
