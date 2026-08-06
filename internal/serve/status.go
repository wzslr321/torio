package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/guestexec"
)

// StatusReport is the structured readiness of the backend. It proves both the
// user-systemd state AND actual HTTP endpoint readiness through loopback: an
// active process with a dead endpoint is NOT ready
// (docs/contracts/cli.md).
type StatusReport struct {
	Installed     bool
	Enabled       bool
	Active        bool
	ActiveState   string // raw `systemctl --user is-active` value
	EndpointReady bool
	EndpointCode  int    // last HTTP status from the loopback probe (0 = no answer)
	Version       string // parsed from /api/status when ready
	Ready         bool   // Active && EndpointReady
	URL           string // the loopback URL probed
}

// sh runs a guest command through the transport and fails closed on a truncated
// result (bounded output that was cut is untrustworthy, per the bootstrap rule).
// A clean non-zero exit is NOT an error here — the caller interprets ExitCode.
func (a *Adapter) sh(ctx context.Context, op string, argv []string) (execx.Result, *Error) {
	res, err := guestexec.Run(ctx, a.Guest, argv)
	switch {
	case errors.Is(err, guestexec.ErrTruncated):
		return execx.Result{}, &Error{Op: op, Kind: KindGuestCommandFailed, Err: err}
	case err != nil:
		return execx.Result{}, fromGuestErr(op, err)
	}
	return res, nil
}

// shInput is sh with a fed stdin (writing the generated unit via `tee`).
func (a *Adapter) shInput(ctx context.Context, op string, stdin []byte, argv []string) (execx.Result, *Error) {
	res, err := guestexec.RunInput(ctx, a.Guest, stdin, argv)
	switch {
	case errors.Is(err, guestexec.ErrTruncated):
		return execx.Result{}, &Error{Op: op, Kind: KindGuestCommandFailed, Err: err}
	case err != nil:
		return execx.Result{}, fromGuestErr(op, err)
	}
	return res, nil
}

// installed reports whether the unit file exists on the guest.
func (a *Adapter) installed(ctx context.Context, op string) (bool, *Error) {
	res, e := a.sh(ctx, op, guestexec.UserExec("test", "-f", unitPath))
	if e != nil {
		return false, e
	}
	return res.ExitCode == 0, nil
}

// activeState returns the raw `systemctl --user is-active` value. is-active
// exits non-zero when the unit is not active; that is not an adapter error.
func (a *Adapter) activeState(ctx context.Context, op, rt string) (string, *Error) {
	res, e := a.sh(ctx, op, userctl(rt, "is-active", UnitName))
	if e != nil {
		return "", e
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// enabledState returns the raw `systemctl --user is-enabled` value ("" if the
// query produced nothing, e.g. a missing unit).
func (a *Adapter) enabledState(ctx context.Context, op, rt string) (string, *Error) {
	res, e := a.sh(ctx, op, userctl(rt, "is-enabled", UnitName))
	if e != nil {
		return "", e
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// probeEndpoint does one loopback HTTP probe of /api/status as the hermes user.
// It returns the observed HTTP status code and the version parsed from the
// response body (empty when absent/unparseable). curl's own non-zero exit (e.g.
// connection refused) is NOT an adapter error — it means the endpoint is down,
// and -w still prints a numeric code ("000"). Only a transport failure is
// returned as an Error. The full body is parsed for the version and then
// discarded — we never carry the raw body (which can be large) around; only the
// short derived version survives.
func (a *Adapter) probeEndpoint(ctx context.Context, op string) (int, string, *Error) {
	argv := guestexec.UserExec("curl", "-s", "-m", "5", "-w", "\n%{http_code}", EndpointURL())
	res, e := a.sh(ctx, op, argv)
	if e != nil {
		return 0, "", e
	}
	out := string(res.Stdout)
	nl := strings.LastIndexByte(out, '\n')
	if nl < 0 {
		// No code separator: treat as no answer.
		return 0, "", nil
	}
	code, _ := strconv.Atoi(strings.TrimSpace(out[nl+1:]))
	version, _ := parseStatusVersion(out[:nl])
	return code, version, nil
}

// parseStatusVersion extracts the top-level "version" from the /api/status JSON.
// A parseable version proves the readiness endpoint answered with real content,
// not merely a socket accept.
func parseStatusVersion(body string) (string, bool) {
	var s struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &s); err != nil {
		return "", false
	}
	if s.Version == "" {
		return "", false
	}
	return s.Version, true
}

// endpointReady is the loopback readiness predicate. A bare HTTP 200 is not
// enough — an unrelated process, stale listener, or proxy can accept the socket
// and return 200 on port 9119. Readiness requires 200 AND a parseable non-empty
// Hermes `version`, proving we reached the real /api/status document.
func endpointReady(code int, version string) bool {
	return code == 200 && version != ""
}

// endpointUnreadyErr builds the KindEndpointUnready message, distinguishing a
// non-200 answer from a 200 that lacked a parseable Hermes version (an
// unexpected listener on the port), so the operator can tell the two apart.
func endpointUnreadyErr(code int, version string) error {
	if code == 200 && version == "" {
		return fmt.Errorf("service active but %s answered 200 without a parseable Hermes version — not the expected backend", StatusPath)
	}
	return fmt.Errorf("service active but %s answered %d, not 200", StatusPath, code)
}

// Status reports the backend's readiness: the unit's installed/enabled state,
// the user-systemd active state, and actual loopback endpoint readiness. It
// always returns a populated report (so the CLI can render the full picture),
// plus a classified error when the service is not fully ready:
//   - not installed        -> KindNotInstalled  (exit 3)
//   - installed, inactive  -> KindInactive       (exit 3)
//   - active, endpoint dead -> KindEndpointUnready (exit 6)
//
// A ready backend returns a nil error.
func (a *Adapter) Status(ctx context.Context) (StatusReport, error) {
	const op = "status"
	rep := StatusReport{URL: EndpointURL()}

	rt, e := a.runtimeDir(ctx, op)
	if e != nil {
		return rep, e
	}

	inst, e := a.installed(ctx, op)
	if e != nil {
		return rep, e
	}
	rep.Installed = inst
	if inst {
		en, e := a.enabledState(ctx, op, rt)
		if e != nil {
			return rep, e
		}
		rep.Enabled = en == "enabled"
	}

	act, e := a.activeState(ctx, op, rt)
	if e != nil {
		return rep, e
	}
	rep.ActiveState = act
	rep.Active = act == "active"

	code, version, e := a.probeEndpoint(ctx, op)
	if e != nil {
		return rep, e
	}
	rep.EndpointCode = code
	rep.EndpointReady = endpointReady(code, version)
	rep.Version = version
	rep.Ready = rep.Active && rep.EndpointReady

	switch {
	case !rep.Installed:
		return rep, &Error{Op: op, Kind: KindNotInstalled, Err: fmt.Errorf("unit %q is not installed; run `torio serve install`", UnitName)}
	case !rep.Active:
		return rep, &Error{Op: op, Kind: KindInactive, Err: fmt.Errorf("service is %q, not active; run `torio serve start`", act)}
	case !rep.EndpointReady:
		return rep, &Error{Op: op, Kind: KindEndpointUnready, Err: endpointUnreadyErr(code, version)}
	}
	return rep, nil
}
