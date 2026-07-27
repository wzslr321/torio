package serve

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

// fakeGuest is a deterministic, local Guest test double. It never reaches a real
// VM: a routing method answers each guest command by matching on the joined
// argv, so serve's branching flows (install, start/stop/restart, status, logs)
// are exercised without Lima. It records every call for argv/stdin assertions
// and holds small counters so state-changing flows (is-active before/after a
// stop, an endpoint that comes up after a few probes) can be modelled.
type fakeGuest struct {
	mu    sync.Mutex
	calls []fakeCall
	env   serveEnv

	activeIdx   int
	endpointIdx int

	// transportErr, if set, is returned by every call (a transport failure).
	transportErr error
}

type fakeCall struct {
	argv   []string
	stdin  []byte
	ctxErr error
	ctx    context.Context
}

func (f *fakeGuest) record(ctx context.Context, argv []string, stdin []byte) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{argv: argv, stdin: stdin, ctxErr: ctx.Err(), ctx: ctx})
	f.mu.Unlock()
}

func (f *fakeGuest) SSH(ctx context.Context, command []string) (execx.Result, error) {
	f.record(ctx, command, nil)
	return f.route(command)
}

func (f *fakeGuest) SSHInput(ctx context.Context, stdin []byte, command []string) (execx.Result, error) {
	f.record(ctx, command, stdin)
	return f.route(command)
}

// nextActive returns the is-active value for the next is-active call, consuming
// env.activeSeq if provided (successive values), else the static env.active.
func (f *fakeGuest) nextActive() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.env.activeSeq) == 0 {
		return f.env.active
	}
	i := f.activeIdx
	if i >= len(f.env.activeSeq) {
		i = len(f.env.activeSeq) - 1
	}
	f.activeIdx++
	return f.env.activeSeq[i]
}

// nextEndpointCode returns the curl http_code for the next probe, consuming
// env.endpointCodeSeq if provided, else the static env.endpointCode.
func (f *fakeGuest) nextEndpointCode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.env.endpointCodeSeq) == 0 {
		return f.env.endpointCode
	}
	i := f.endpointIdx
	if i >= len(f.env.endpointCodeSeq) {
		i = len(f.env.endpointCodeSeq) - 1
	}
	f.endpointIdx++
	return f.env.endpointCodeSeq[i]
}

func (f *fakeGuest) route(argv []string) (execx.Result, error) {
	if f.transportErr != nil {
		return execx.Result{ExitCode: -1}, f.transportErr
	}
	e := f.env
	j := strings.Join(argv, " ")
	switch {
	case strings.Contains(j, "id -u "+lima.HermesUser):
		return stdoutR(e.uid + "\n"), nil
	case strings.Contains(j, "loginctl show-user"):
		if e.lingerYes {
			return stdoutR("Linger=yes\n"), nil
		}
		return stdoutR("Linger=no\n"), nil
	case strings.Contains(j, "loginctl enable-linger"):
		return exitR(0, "", ""), nil
	case strings.Contains(j, "mkdir -p"):
		return exitR(0, "", ""), nil
	case strings.Contains(j, "tee "+stagingPath):
		return exitR(0, "", ""), nil
	case strings.Contains(j, "systemd-analyze --user verify"):
		if e.verifyOK {
			return exitR(0, "", ""), nil
		}
		return exitR(1, "", "unit has a bad setting\n"), nil
	case strings.Contains(j, "mv -f "+stagingPath):
		return exitR(0, "", ""), nil
	case strings.Contains(j, "rm -f "+stagingPath):
		return exitR(0, "", ""), nil
	case strings.Contains(j, "cat "+unitPath):
		if e.existingAbsent {
			return exitR(1, "", "No such file\n"), nil
		}
		return stdoutR(e.existing), nil
	case strings.Contains(j, "test -f "+unitPath):
		if e.installed {
			return exitR(0, "", ""), nil
		}
		return exitR(1, "", ""), nil
	case strings.Contains(j, "systemctl --user daemon-reload"):
		return exitR(0, "", ""), nil
	case strings.Contains(j, "systemctl --user is-enabled"):
		code := 0
		if e.enabled != "enabled" {
			code = 1
		}
		return exitR(code, e.enabled+"\n", ""), nil
	case strings.Contains(j, "systemctl --user enable"):
		return exitR(0, "", ""), nil
	case strings.Contains(j, "systemctl --user is-active"):
		v := f.nextActive()
		code := 0
		if v != "active" {
			code = 3
		}
		return exitR(code, v+"\n", ""), nil
	case strings.Contains(j, "systemctl --user start"),
		strings.Contains(j, "systemctl --user stop"),
		strings.Contains(j, "systemctl --user restart"):
		if !e.verbOK {
			return exitR(1, "", "Job failed\n"), nil
		}
		return exitR(0, "", ""), nil
	case strings.Contains(j, "journalctl"):
		return stdoutR(e.journal), nil
	case strings.Contains(j, "curl"):
		// curl -w "\n%{http_code}" prints body then a newline then the code.
		return stdoutR(e.endpointBody + "\n" + f.nextEndpointCode()), nil
	}
	return exitR(0, "", ""), fmt.Errorf("fakeGuest: unrouted command: %s", j)
}

func (f *fakeGuest) joinedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = strings.Join(c.argv, " ")
	}
	return out
}

// sawCommand reports whether any recorded call's joined argv contains sub.
func (f *fakeGuest) sawCommand(sub string) bool {
	for _, j := range f.joinedCalls() {
		if strings.Contains(j, sub) {
			return true
		}
	}
	return false
}

// indexOf returns the index of the first recorded call whose joined argv
// contains sub, or -1.
func (f *fakeGuest) indexOf(sub string) int {
	for i, j := range f.joinedCalls() {
		if strings.Contains(j, sub) {
			return i
		}
	}
	return -1
}

// stdinFor returns the stdin fed to the first call whose joined argv contains sub.
func (f *fakeGuest) stdinFor(sub string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c.argv, " "), sub) {
			return c.stdin, true
		}
	}
	return nil, false
}

// serveEnv configures the fake guest's responses. defaultEnv() is a fully
// installed, active, ready backend; tests flip individual fields to drive a
// specific branch.
type serveEnv struct {
	uid             string
	lingerYes       bool
	installed       bool
	existingAbsent  bool     // `cat unit` returns exit 1 (no file yet)
	existing        string   // `cat unit` body when present
	verifyOK        bool     // systemd-analyze verify passes
	active          string   // static is-active value
	activeSeq       []string // successive is-active values (overrides active)
	enabled         string   // is-enabled value
	verbOK          bool     // systemctl start/stop/restart exit 0
	endpointCode    string   // static curl %{http_code}
	endpointCodeSeq []string // successive curl codes (overrides endpointCode)
	endpointBody    string   // curl body
	journal         string   // journalctl output
}

func defaultEnv() serveEnv {
	return serveEnv{
		uid:          "1000",
		lingerYes:    true,
		installed:    true,
		existing:     string(renderUnit()),
		verifyOK:     true,
		active:       "active",
		enabled:      "enabled",
		verbOK:       true,
		endpointCode: "200",
		// A realistic /api/status body: >200 chars, version NOT first, so a naive
		// truncate-before-parse would drop the version. Locks the live-found bug.
		endpointBody: `{"release_date":"2026.7.20","config_version":33,"latest_config_version":33,"can_update_hermes":true,"gateway_running":false,"gateway_state":"stopped","active_sessions":0,"auth_required":false,"overall":"degraded","version":"0.19.0","profiles":["default"]}`,
		journal:      "-- No entries --\n",
	}
}

func stdoutR(s string) execx.Result { return execx.Result{ExitCode: 0, Stdout: []byte(s)} }
func exitR(code int, out, errs string) execx.Result {
	return execx.Result{ExitCode: code, Stdout: []byte(out), Stderr: []byte(errs)}
}

func newFake(e serveEnv) *fakeGuest { return &fakeGuest{env: e} }
