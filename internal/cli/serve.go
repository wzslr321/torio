package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/serve"
)

// newServeCmd builds the `torio serve` parent and its lifecycle subcommands for the
// loopback-only Hermes backend (Demo A D5). Like `torio vm`, the parent takes no
// action itself: an absent/unknown subcommand is a usage error.
func newServeCmd(a *app) *cobra.Command {
	s := &cobra.Command{
		Use:   "serve",
		Short: "Manage the loopback-only Hermes Desktop backend",
		Long: "Install and control the Hermes backend (`hermes serve`) as a custom user " +
			"systemd service on the Torio VM. It binds guest loopback only " +
			"(127.0.0.1:9119) using the existing /home/hermes/.hermes profile; the Mac " +
			"reaches it through an operator-controlled SSH tunnel (docs/contracts/cli.md). " +
			"Torio adds no tunnel of its own.",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError("no subcommand given; run 'torio serve --help'")
			}
			return usageError(fmt.Sprintf("unknown serve subcommand %q", args[0]))
		},
	}
	s.AddCommand(newServeInstallCmd(a))
	s.AddCommand(newServeStartCmd(a))
	s.AddCommand(newServeStopCmd(a))
	s.AddCommand(newServeRestartCmd(a))
	s.AddCommand(newServeStatusCmd(a))
	s.AddCommand(newServeLogsCmd(a))
	return s
}

func newServeInstallCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Generate, validate, and enable the backend user service",
		Long: "Ensure user linger, render the custom user unit (loopback bind, HERMES_HOME " +
			"profile pin, Restart=always), validate it with systemd-analyze BEFORE activation, " +
			"then reload and enable it for boot. Idempotent; accepts no secrets; does not start " +
			"the backend (use `torio serve start`). Runs several bounded guest probes — use an ample " +
			"--timeout (e.g. --timeout 2m).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			rep, err := a.newServe().Install(ctx)
			if err != nil {
				return mapServeError("serve.install", err)
			}
			return a.emitServeInstall(rep)
		},
	}
}

func newServeStartCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the backend and prove loopback readiness",
		Long: "Start the backend service, then fail closed unless BOTH the re-queried systemd " +
			"state is active AND the loopback /api/status endpoint answers 200 — an active " +
			"process with a dead endpoint is a failure. Idempotent. Use an ample --timeout " +
			"(e.g. --timeout 2m) so the endpoint has time to bind.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			rep, err := a.newServe().Start(ctx)
			if err != nil {
				ce := mapServeError("serve.start", err)
				ce.Details = serveStatusDetails(rep)
				return ce
			}
			return a.emitServeStatus("serve.start", rep)
		},
	}
}

func newServeStopCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the backend service",
		Long: "Stop the backend gracefully. Idempotent (already-inactive succeeds). Does not " +
			"trust a clean exit — it re-queries is-active and requires a non-active post-state. " +
			"Never removes the unit, profile, or state.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			rep, err := a.newServe().Stop(ctx)
			if err != nil {
				return mapServeError("serve.stop", err)
			}
			return a.emitServeStop(rep)
		},
	}
}

func newServeRestartCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the backend and prove loopback readiness",
		Long: "Restart the backend, then verify the same postconditions as start (active AND " +
			"/api/status answering 200). Session/state persistence is the backend's own " +
			"responsibility. Use an ample --timeout (e.g. --timeout 2m).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			rep, err := a.newServe().Restart(ctx)
			if err != nil {
				ce := mapServeError("serve.restart", err)
				ce.Details = serveStatusDetails(rep)
				return ce
			}
			return a.emitServeStatus("serve.restart", rep)
		},
	}
}

func newServeStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report backend systemd state and loopback endpoint readiness",
		Long: "Prove BOTH the user-systemd state and actual HTTP readiness through loopback. " +
			"Exits 0 only when the service is active AND /api/status answers 200; a not-installed " +
			"or inactive service exits 3, and an active service with a dead endpoint exits 6.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			rep, err := a.newServe().Status(ctx)
			if err != nil {
				ce := mapServeError("serve.status", err)
				ce.Details = serveStatusDetails(rep)
				return ce
			}
			return a.emitServeStatus("serve.status", rep)
		},
	}
}

func newServeLogsCmd(a *app) *cobra.Command {
	var lines int
	c := &cobra.Command{
		Use:   "logs",
		Short: "Show recent, bounded backend service logs",
		Long: "Show the last N journal entries for the backend unit via journalctl --user. " +
			"Output is bounded (by -n and the runner's per-stream cap) and redacted, and is " +
			"scoped to this unit's own stdout/stderr — never profile, KB, or provider data.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			rep, err := a.newServe().Logs(ctx, lines)
			if err != nil {
				return mapServeError("serve.logs", err)
			}
			return a.emitServeLogs(rep)
		},
	}
	c.Flags().IntVar(&lines, "lines", serve.DefaultLogLines, "number of recent journal lines (bounded)")
	return c
}

// --- envelope data ---

type serveInstallData struct {
	UnitPath      string `json:"unit_path"`
	Changed       bool   `json:"changed"`
	LingerEnabled bool   `json:"linger_enabled"`
	Validated     bool   `json:"validated"`
	Enabled       bool   `json:"enabled"`
}

type serveStatusData struct {
	Installed     bool   `json:"installed"`
	Enabled       bool   `json:"enabled"`
	Active        bool   `json:"active"`
	ActiveState   string `json:"active_state"`
	EndpointReady bool   `json:"endpoint_ready"`
	EndpointCode  int    `json:"endpoint_code"`
	Version       string `json:"version"`
	Ready         bool   `json:"ready"`
	URL           string `json:"url"`
}

type serveStopData struct {
	Active      bool   `json:"active"`
	ActiveState string `json:"active_state"`
}

type serveLogsData struct {
	Unit  string `json:"unit"`
	Lines int    `json:"lines"`
	Text  string `json:"text"`
}

func statusData(r serve.StatusReport) serveStatusData {
	return serveStatusData{
		Installed:     r.Installed,
		Enabled:       r.Enabled,
		Active:        r.Active,
		ActiveState:   r.ActiveState,
		EndpointReady: r.EndpointReady,
		EndpointCode:  r.EndpointCode,
		Version:       r.Version,
		Ready:         r.Ready,
		URL:           r.URL,
	}
}

// serveStatusDetails renders a StatusReport as error details so a not-ready
// start/restart/status still tells the operator exactly which postcondition
// failed. Values pass through the final redactor in fail().
func serveStatusDetails(r serve.StatusReport) map[string]any {
	return map[string]any{
		"installed":      r.Installed,
		"enabled":        r.Enabled,
		"active":         r.Active,
		"active_state":   r.ActiveState,
		"endpoint_ready": r.EndpointReady,
		"endpoint_code":  r.EndpointCode,
		"ready":          r.Ready,
		"url":            r.URL,
	}
}

func (a *app) emitServeInstall(rep serve.InstallReport) error {
	if a.jsonOut {
		data := serveInstallData{
			UnitPath:      rep.UnitPath,
			Changed:       rep.Changed,
			LingerEnabled: rep.LingerEnabled,
			Validated:     rep.Validated,
			Enabled:       rep.Enabled,
		}
		return writeJSON(a.stdout, successEnvelope("serve.install", data))
	}
	state := "unchanged"
	if rep.Changed {
		state = "installed"
	}
	_, err := fmt.Fprintf(a.stdout,
		"Backend user service %s (%s).\n"+
			"  unit:           %s\n"+
			"  linger enabled: %t\n"+
			"  validated:      %t\n"+
			"  enabled (boot): %t\n"+
			"Next: torio serve start\n",
		serve.UnitName, state, rep.UnitPath, rep.LingerEnabled, rep.Validated, rep.Enabled)
	return err
}

func (a *app) emitServeStatus(command string, rep serve.StatusReport) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope(command, statusData(rep)))
	}
	_, err := fmt.Fprintf(a.stdout,
		"Backend ready on %s\n"+
			"  systemd:  %s (active=%t, enabled=%t)\n"+
			"  endpoint: %d (ready=%t)\n"+
			"  version:  %s\n",
		rep.URL, rep.ActiveState, rep.Active, rep.Enabled, rep.EndpointCode, rep.EndpointReady, rep.Version)
	return err
}

func (a *app) emitServeStop(rep serve.StopReport) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("serve.stop", serveStopData{Active: rep.Active, ActiveState: rep.ActiveState}))
	}
	_, err := fmt.Fprintf(a.stdout, "Backend %s stopped (state=%s).\n", serve.UnitName, rep.ActiveState)
	return err
}

func (a *app) emitServeLogs(rep serve.LogsReport) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("serve.logs", serveLogsData{Unit: rep.Unit, Lines: rep.Lines, Text: rep.Text}))
	}
	// Human mode: write the (bounded, redacted) log text verbatim to stdout.
	_, err := a.stdout.Write([]byte(rep.Text))
	return err
}

// mapServeError maps a *serve.Error onto the CLI exit-code contract via
// ErrorKind (never string matching), mirroring mapLimaError.
func mapServeError(command string, err error) *CLIError {
	var serr *serve.Error
	if !errors.As(err, &serr) {
		e := internalError(err.Error())
		e.Command = command
		return e
	}
	code := strings.ToUpper(string(serr.Kind))
	switch serr.Kind {
	case serve.KindNotInstalled, serve.KindInactive, serve.KindPostconditionFailed:
		return &CLIError{Exit: ExitPrecondition, Code: code, Command: command, Message: serr.Error()}
	case serve.KindEndpointUnready, serve.KindValidationFailed:
		return &CLIError{Exit: ExitVerification, Code: code, Command: command, Message: serr.Error()}
	case serve.KindTransport, serve.KindTimeout, serve.KindCancelled, serve.KindGuestCommandFailed:
		return &CLIError{Exit: ExitExternal, Code: code, Command: command, Message: serr.Error()}
	default:
		e := internalError(serr.Error())
		e.Command = command
		return e
	}
}
