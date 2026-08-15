package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/redact"
)

type mcpMsg struct {
	report lima.MCPBrokerReport
	err    error
}

// mcpInstallMsg is a finished provisioning. It is its own message rather than
// the generic operation outcome because a successful install can still leave
// one thing to do — the restart — and that has to reach the note.
type mcpInstallMsg struct {
	report lima.MCPBrokerInstallReport
	err    error
}

// mcpScreen is `mcp status` rendered, with the two actions the boundary has:
// provision it, and sign one policy service in. What it shows is what the
// command shows — the checks, the identity separation they establish, and the
// grant — and never a credential, which Torio does not hold on any path here
// (ADR-0004).
type mcpScreen struct {
	report lima.MCPBrokerReport
	loaded bool
	failed string

	// loginPick is the service chooser in front of `l`. The names come from
	// the verified grant, which is the same set `mcp login <service>` accepts,
	// so the picker cannot offer a service the command would refuse.
	loginPick   bool
	loginCursor int
}

func (s *mcpScreen) load(d Deps) tea.Cmd {
	if d.MCPStatus == nil {
		return nil
	}
	timeout := d.Timeout
	statusFn := d.MCPStatus
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(d.parentContext(), longOr(timeout))
		defer cancel()
		rep, err := statusFn(ctx)
		return mcpMsg{report: rep, err: err}
	}
}

func (s *mcpScreen) update(r *root, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case mcpMsg:
		s.loaded = true
		s.report = msg.report
		s.failed = ""
		if msg.err != nil {
			// A boundary that does not hold, or was never provisioned, is a
			// state of this screen rather than a hub failure: the screen
			// exists to say it and to offer the repair.
			s.failed = redact.String(msg.err.Error())
		}
		return nil

	case mcpInstallMsg:
		r.busy = ""
		if msg.err != nil {
			r.errText = "provisioning the MCP boundary: " + redact.String(msg.err.Error())
			return nil
		}
		note := "MCP boundary provisioned"
		if !msg.report.Changed {
			note = "MCP boundary already provisioned; nothing changed"
		}
		if msg.report.RestartRequired {
			// The one thing left to do, said where the operator is: a running
			// process does not gain a group under itself.
			note += "; restart the backend service before expecting the agent to reach the broker"
		}
		r.note = note
		return tea.Batch(r.probeFacts(), s.load(r.deps))

	case tea.KeyMsg:
		if s.loginPick {
			return s.updateLoginPick(r, msg)
		}
		d := r.deps
		switch msg.String() {
		case "i":
			return s.startInstall(r)
		case "l":
			if d.MCPLoginSpec != nil && len(s.report.Policy.Services) > 0 {
				s.loginPick = true
				s.loginCursor = 0
			}
		}
	}
	return nil
}

// startInstall runs the provisioning under the busy lock. Long: it creates an
// identity, installs units, wires the backend, and verifies.
func (s *mcpScreen) startInstall(r *root) tea.Cmd {
	if r.busy != "" || r.deps.MCPInstall == nil {
		return nil
	}
	r.busy = "provisioning the MCP boundary"
	r.busyStart = time.Now()
	r.errText = ""
	r.errDetail = ""
	r.note = ""
	d := r.deps
	return func() tea.Msg {
		ctx, cancel := r.opContext(true)
		defer cancel()
		rep, err := d.MCPInstall(ctx)
		return mcpInstallMsg{report: rep, err: err}
	}
}

func (s *mcpScreen) updateLoginPick(r *root, msg tea.KeyMsg) tea.Cmd {
	services := s.report.Policy.Services
	switch msg.String() {
	case "esc":
		s.loginPick = false
	case "up", "k":
		if s.loginCursor > 0 {
			s.loginCursor--
		}
	case "down", "j":
		if s.loginCursor < len(services)-1 {
			s.loginCursor++
		}
	case "enter":
		s.loginPick = false
		service := services[s.loginCursor].Name
		d := r.deps
		// The activation is what `mcp login` runs when its session ends: the
		// broker starts once every policy service holds a private session. It
		// is registered before the handoff and runs only on a clean end — a
		// failed login stored nothing to activate.
		if d.MCPActivate != nil {
			activate := d.MCPActivate
			r.afterSession = func() tea.Cmd {
				return r.runDetailed("activating the broker", false, func(ctx context.Context) (string, error) {
					rep, err := activate(ctx)
					if err != nil {
						return "", err
					}
					if rep.Activated {
						r.note = "broker active: every policy service is signed in"
					} else {
						r.note = fmt.Sprintf("%s signed in; %d policy service(s) still require login before the broker starts", service, rep.Pending)
					}
					return "", nil
				})
			}
		}
		return r.handoff("MCP login: "+service, func(context.Context) (execx.InteractiveCommand, error) {
			return d.MCPLoginSpec(service)
		})
	}
	return nil
}

func (s *mcpScreen) keys(r *root) string {
	if s.loginPick {
		return "↑/↓ pick · enter login · esc cancel"
	}
	parts := []string{}
	if r.deps.MCPInstall != nil {
		parts = append(parts, "i install")
	}
	if r.deps.MCPLoginSpec != nil && len(s.report.Policy.Services) > 0 {
		parts = append(parts, "l login")
	}
	return strings.Join(parts, " · ")
}

func (s *mcpScreen) view(r *root, w int) string {
	if s.loginPick {
		var b strings.Builder
		b.WriteString(styStrong.Render("Sign one policy service in") + "\n")
		b.WriteString(styMuted.Render("The OAuth session runs as the broker identity; Torio never sees the token.") + "\n\n")
		for i, svc := range s.report.Policy.Services {
			if i == s.loginCursor {
				b.WriteString(styWorking.Render("▸ ") + styText.Render(svc.Name) + "\n")
				continue
			}
			b.WriteString("  " + styText.Render(svc.Name) + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}

	if !s.loaded {
		return styMuted.Render("Proving the MCP boundary…")
	}

	var b strings.Builder
	if s.failed != "" {
		b.WriteString(styAmber.Render("the boundary does not hold: ") + styText.Render(s.failed) + "\n")
		if r.deps.MCPInstall != nil {
			b.WriteString("\n" + styBtn.Render("Provision") + styDim.Render("  press i"))
		}
		return renderMCPChecks(&b, s.report)
	}

	b.WriteString(styStrong.Render("MCP boundary") + "  " + styLive.Render("holds") + "\n\n")
	rep := s.report
	agentUser := rep.AgentUser
	if agentUser == "" {
		agentUser = lima.HermesUser
	}
	b.WriteString(line(true, "credential owner", lima.TorioMCPUser+" (home "+lima.TorioMCPHome+", readable by nobody else)"))
	b.WriteString(line(true, "agent identity", agentUser+" — may open the broker socket, cannot read its credentials"))
	b.WriteString(line(true, "client group", lima.TorioMCPClientsGroup))
	return renderMCPChecks(&b, rep)
}

// renderMCPChecks appends the proven checks and the grant, which are safe to
// print for the reason the command prints them: a check name and detail are
// derived markers, and a service name and endpoint are schema-validated before
// they reach a report. A guest filename never appears in either.
func renderMCPChecks(b *strings.Builder, rep lima.MCPBrokerReport) string {
	if len(rep.Checks) > 0 {
		b.WriteString("\n")
		for _, c := range rep.Checks {
			b.WriteString(line(c.OK, c.Name, c.Detail))
		}
	}
	if len(rep.Policy.Services) > 0 {
		b.WriteString("\n" + styMuted.Render("granted policy (generation "+rep.Policy.Digest+")") + "\n")
		for _, svc := range rep.Policy.Services {
			fmt.Fprintf(b, "  %s  %d tool(s), %d write  ->  %s\n",
				styText.Render(svc.Name), svc.Tools, svc.WriteTools, svc.UpstreamEndpoint)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
