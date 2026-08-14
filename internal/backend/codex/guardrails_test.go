package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// managedConfigProbes is a guest whose managed configuration is already the one
// Torio installs.
func managedConfigProbes() map[string]execx.Result {
	return map[string]execx.Result{
		"stat -c %F " + managedConfigPath:       out("regular file\n"),
		"stat -c %U:%G %a " + managedConfigPath: out("root:root 644\n"),
		"sha256sum -- " + managedConfigPath:     out(digestOf(embeddedManagedConfig) + "  " + managedConfigPath + "\n"),
	}
}

// TestManagedConfigIsInstalledWhenAbsentAndProvenAfterwards pins the one repair
// this guardrail performs. A file that is not there yet has nothing to drift
// from, so installing it is not overwriting anybody's decision.
func TestManagedConfigIsInstalledWhenAbsentAndProvenAfterwards(t *testing.T) {
	probes := managedConfigProbes()
	// stat on a missing path: exit 1, nothing on stdout.
	probes["stat -c %F "+managedConfigPath] = exit(1)
	probes["test ! -e "+managedConfigPath] = exit(0)
	install := strings.Join(managedConfigInstallArgv(), " ")
	probes[install] = exit(0)
	r := newFakeRunner(probes)

	// The path exists only after the install runs, which is what the second stat
	// has to see for this to be a reconcile rather than a check that got lucky.
	installed := false
	r.onCall = func(argv string) {
		if argv == install {
			installed = true
			r.answers["stat -c %F "+managedConfigPath] = out("regular file\n")
		}
	}

	if err := reconcileManagedConfig(context.Background(), r); err != nil {
		t.Fatalf("reconcileManagedConfig on a fresh guest: %v", err)
	}
	if !installed {
		t.Fatal("the absent managed configuration was never installed")
	}
	if got := r.records["codex_managed_config"]; !strings.Contains(got, "root:root") {
		t.Errorf("recorded %q, want it to state the ownership it proved", got)
	}
}

// TestManagedConfigDriftIsReportedNeverRepaired is the rule that makes this file
// worth root-owning. A guardrail that rewrites itself is a guardrail whose
// changes nobody reviews.
func TestManagedConfigDriftIsReportedNeverRepaired(t *testing.T) {
	probes := managedConfigProbes()
	probes["sha256sum -- "+managedConfigPath] = out(strings.Repeat("a", 64) + "  " + managedConfigPath + "\n")
	r := newFakeRunner(probes)

	if err := reconcileManagedConfig(context.Background(), r); err == nil {
		t.Fatal("a drifted managed configuration passed")
	}
	if r.saw("/bin/bash -ceu") {
		t.Error("drift was repaired in place instead of being reported")
	}
	if !strings.Contains(r.remediation, managedConfigPath) {
		t.Errorf("the remediation does not name the file to inspect: %q", r.remediation)
	}
}

// TestManagedConfigMustNotBeSomethingTheAgentCanRewrite pins the ownership and
// the file kind. Settings the agent owns are settings the agent can retune
// between sessions, and a symlink moves them somewhere it may own.
func TestManagedConfigMustNotBeSomethingTheAgentCanRewrite(t *testing.T) {
	for _, tc := range []struct{ name, key, answer string }{
		{"owned by the agent", "stat -c %U:%G %a " + managedConfigPath, "codex:codex 644\n"},
		{"group-writable", "stat -c %U:%G %a " + managedConfigPath, "root:root 664\n"},
		{"a symlink", "stat -c %F " + managedConfigPath, "symbolic link\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probes := managedConfigProbes()
			probes[tc.key] = out(tc.answer)
			r := newFakeRunner(probes)
			if err := reconcileManagedConfig(context.Background(), r); err == nil {
				t.Fatalf("%s passed as a root-owned guardrail", tc.name)
			}
		})
	}
}

// TestManagedConfigCarriesTheHooksTheMarkerNeeds pins the halves together. The
// helper is installed because the configuration names it; a configuration that
// stopped naming it would leave a helper nothing runs and a status that reports
// waiting as unknown forever.
func TestManagedConfigCarriesTheHooksTheMarkerNeeds(t *testing.T) {
	doc := string(embeddedManagedConfig)

	for _, event := range []string{"Stop", "PermissionRequest", "UserPromptSubmit", "SessionEnd"} {
		if !strings.Contains(doc, "[[hooks."+event+"]]") {
			t.Errorf("the managed configuration declares no %s hook", event)
		}
	}
	if !strings.Contains(doc, WaitingMarkerHelper+" set") {
		t.Error("nothing in the managed configuration sets the waiting marker")
	}
	if !strings.Contains(doc, WaitingMarkerHelper+" clear") {
		t.Error("nothing in the managed configuration clears the waiting marker")
	}
	// Prompts are off because the boundary is the box, not because a prompt was
	// inconvenient. If this ever reads differently, the reasoning in the package
	// comment stopped being true and the change needs its own record.
	if !strings.Contains(doc, `approval_policy = "never"`) {
		t.Error("the managed configuration does not turn approval prompts off")
	}
	if !strings.Contains(doc, "check_for_update_on_startup = false") {
		t.Error("the managed configuration lets the pinned binary shop for a newer one")
	}
}

// TestMCPReportingNeverFailsAndNamesOnly pins the compensating legibility for the
// hole this backend accepts. The source is a file the agent owns, so the check
// cannot be a boundary; what it must do is report, never fail, and never carry a
// value that could be a token.
func TestMCPReportingNeverFailsAndNamesOnly(t *testing.T) {
	present := "sudo -n -u " + User + " -- test -f " + agentConfigPath
	read := "sudo -n -u " + User + " -- python3 -c " + mcpNamesProgram + " " + agentConfigPath

	t.Run("no configuration is a state, not a failure", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{present: exit(1)})
		if err := reportMCPServers(context.Background(), r); err != nil {
			t.Fatalf("reportMCPServers: %v", err)
		}
		if got := r.records[mcpServersCheck]; !strings.Contains(got, "none") {
			t.Errorf("recorded %q, want it to say none configured", got)
		}
	})

	t.Run("configured servers are reported as unverified", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{present: exit(0), read: out("atlassian\nslack\n")})
		if err := reportMCPServers(context.Background(), r); err != nil {
			t.Fatalf("reportMCPServers: %v", err)
		}
		got := r.records[mcpServersCheck]
		for _, want := range []string{"atlassian", "slack", "not verified"} {
			if !strings.Contains(got, want) {
				t.Errorf("recorded %q, want it to contain %q", got, want)
			}
		}
	})

	t.Run("an unreadable agent-owned document is not a failure", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{present: exit(0), read: exit(1)})
		if err := reportMCPServers(context.Background(), r); err != nil {
			t.Fatalf("reportMCPServers failed over a file the agent owns: %v", err)
		}
	})
}
