package serve

import (
	"context"
	"strings"
	"testing"
)

func TestStatusReadyBackend(t *testing.T) {
	f := newFake(defaultEnv())
	a := New(f)

	rep, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: unexpected error for a ready backend: %v", err)
	}
	if !rep.Ready || !rep.Installed || !rep.Enabled || !rep.Active || !rep.EndpointReady {
		t.Fatalf("report = %+v, want fully ready", rep)
	}
	if rep.Version != "0.19.0" {
		t.Errorf("Version = %q, want 0.19.0", rep.Version)
	}
	// Status must prove BOTH systemd state and the HTTP endpoint.
	if !f.sawCommand("systemctl --user is-active") || !f.sawCommand("curl") {
		t.Errorf("status must query both systemd state and the loopback endpoint")
	}
}

func TestStatusNotInstalled(t *testing.T) {
	env := defaultEnv()
	env.installed = false
	f := newFake(env)
	a := New(f)

	rep, err := a.Status(context.Background())
	assertKind(t, err, KindNotInstalled)
	if rep.Installed {
		t.Errorf("Installed = true, want false")
	}
}

func TestStatusInstalledButInactive(t *testing.T) {
	env := defaultEnv()
	env.active = "inactive"
	f := newFake(env)
	a := New(f)

	rep, err := a.Status(context.Background())
	assertKind(t, err, KindInactive)
	if !rep.Installed || rep.Active {
		t.Errorf("report = %+v, want Installed && !Active", rep)
	}
}

func TestStatusActiveButEndpointDeadIsFailure(t *testing.T) {
	// The core service-lifecycle invariant: an active process with a dead
	// endpoint is NOT ready and must be surfaced as a verification failure.
	env := defaultEnv()
	env.active = "active"
	env.endpointCode = "000"
	f := newFake(env)
	a := New(f)

	rep, err := a.Status(context.Background())
	assertKind(t, err, KindEndpointUnready)
	if !rep.Active {
		t.Errorf("Active = false, want true")
	}
	if rep.EndpointReady || rep.Ready {
		t.Errorf("must not report ready when the endpoint is dead: %+v", rep)
	}
}

func TestStatusParsesVersionFromRealisticBody(t *testing.T) {
	// Regression: the /api/status body is larger than any internal detail cap and
	// carries the version well past the first bytes. The version must be parsed
	// from the FULL body, never a truncated prefix.
	f := newFake(defaultEnv())
	a := New(f)
	rep, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if rep.Version != "0.19.0" {
		t.Fatalf("Version = %q, want 0.19.0 (parsed from the full body)", rep.Version)
	}
}

func TestStatus200WithoutVersionIsNotReady(t *testing.T) {
	// Blocking-fix regression: a bare HTTP 200 does not prove this is Hermes
	// /api/status. An unrelated process / stale listener / proxy returning 200 on
	// port 9119 must NOT mark the backend ready. Readiness requires 200 AND a
	// parseable non-empty Hermes version; a 200 without one is endpoint-unready.
	for _, tc := range []struct {
		name string
		body string
	}{
		{"no version field", `{"nope":true,"overall":"ok"}`},
		{"empty version", `{"version":"","overall":"ok"}`},
		{"not json at all", `<html>hello from some other server</html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := defaultEnv()
			env.active = "active"
			env.endpointCode = "200"
			env.endpointBody = tc.body
			f := newFake(env)
			a := New(f)

			rep, err := a.Status(context.Background())
			assertKind(t, err, KindEndpointUnready)
			if !rep.Active {
				t.Errorf("Active = false, want true")
			}
			if rep.EndpointReady || rep.Ready {
				t.Errorf("must not report ready for 200 without a Hermes version: %+v", rep)
			}
			if rep.Version != "" {
				t.Errorf("Version = %q, want empty for a body without version", rep.Version)
			}
			if !strings.HasPrefix(rep.URL, "http://127.0.0.1:9119/api/status") {
				t.Errorf("URL = %q, want the loopback status URL", rep.URL)
			}
		})
	}
}
