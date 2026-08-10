package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wzslr321/torio/internal/redact"
	"github.com/wzslr321/torio/internal/serve"
)

// logLines is how much of the guest journal one fetch asks for. It is bounded
// because the transport bounds it anyway, and an unbounded ask would be
// truncated somewhere less honest than here.
const logLines = 200

type serveMsg struct {
	report serve.StatusReport
	err    error
}

type logsMsg struct {
	text string
	err  error
}

// serveScreen is the guest service: what it is doing, and its journal.
//
// The journal is a snapshot, not a stream. The transport fetches a bounded
// number of lines when asked, so the screen says "snapshot" and offers a
// re-fetch rather than animating a tail it is not receiving.
type serveScreen struct {
	report serve.StatusReport
	loaded bool
	failed string

	logs     viewport.Model
	showLogs bool
	logsErr  string
}

func newServeScreen() serveScreen {
	vp := viewport.New(80, 12)
	return serveScreen{logs: vp}
}

func (s *serveScreen) load(d Deps) tea.Cmd {
	if d.ServeStatus == nil {
		return nil
	}
	timeout := d.Timeout
	statusFn := d.ServeStatus
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), longOr(timeout))
		defer cancel()
		rep, err := statusFn(ctx)
		return serveMsg{report: rep, err: err}
	}
}

func (s *serveScreen) fetchLogs(d Deps) tea.Cmd {
	if d.ServeLogs == nil {
		return nil
	}
	timeout := d.Timeout
	logs := d.ServeLogs
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), longOr(timeout))
		defer cancel()
		text, err := logs(ctx, logLines)
		return logsMsg{text: text, err: err}
	}
}

func (s *serveScreen) update(r *root, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case serveMsg:
		s.loaded = true
		if msg.err != nil {
			s.failed = redact.String(msg.err.Error())
			return nil
		}
		s.failed = ""
		s.report = msg.report
		return nil

	case logsMsg:
		if msg.err != nil {
			s.logsErr = redact.String(msg.err.Error())
			return nil
		}
		s.logsErr = ""
		s.logs.SetContent(msg.text)
		s.logs.GotoBottom()
		return nil

	case tea.KeyMsg:
		d := r.deps
		if !d.ServiceDeclared {
			return nil
		}
		switch msg.String() {
		case "i":
			return r.run("installing the service", true, d.ServeInstall)
		case "s":
			return r.run("starting the service", true, d.ServeStart)
		case "x":
			return r.run("stopping the service", true, d.ServeStop)
		case "t":
			return r.run("restarting the service", true, d.ServeRestart)
		case "l":
			s.showLogs = !s.showLogs
			if s.showLogs {
				return s.fetchLogs(d)
			}
			return nil
		}
		if s.showLogs {
			var cmd tea.Cmd
			s.logs, cmd = s.logs.Update(msg)
			return cmd
		}
	}
	return nil
}

func (s *serveScreen) keys() string {
	if s.showLogs {
		return "↑/↓ scroll · l hide logs · r re-fetch"
	}
	return "i install · s start · x stop · t restart · l logs"
}

func (s *serveScreen) view(r *root, w int) string {
	if !r.deps.ServiceDeclared {
		return styMuted.Render("This backend runs no guest service.") + "\n\n" +
			styDim.Render("Nothing is missing: its interactive surface is a session, not a daemon.")
	}

	switch {
	case s.failed != "":
		return styAmber.Render("the service could not be read: ") + styText.Render(s.failed)
	case !s.loaded:
		return styMuted.Render("Reading the service…")
	}

	rep := s.report
	var b strings.Builder
	b.WriteString(serviceWord(rep) + "\n")
	if rep.URL != "" {
		b.WriteString(styMuted.Render("endpoint ") + styText.Render(rep.URL))
		if rep.Version != "" {
			b.WriteString(styDim.Render("  version "+rep.Version) + "\n")
		} else {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(line(rep.Installed, "unit installed", ternary(rep.Installed, "yes", "no")))
	b.WriteString(line(rep.Enabled, "enabled at boot", ternary(rep.Enabled, "yes", "no")))
	b.WriteString(line(rep.Active, "process", orUnknown(rep.ActiveState)))
	b.WriteString(line(rep.EndpointReady, "endpoint", endpointWord(rep)))

	if s.showLogs {
		width := w - 4
		if width < 20 {
			width = 20
		}
		s.logs.Width = width
		b.WriteString("\n")
		if s.logsErr != "" {
			b.WriteString(styAmber.Render("the journal could not be read: ") + styText.Render(s.logsErr))
			return b.String()
		}
		b.WriteString(styMuted.Render(fmt.Sprintf("journal snapshot, last %d lines", logLines)) + "\n")
		b.WriteString(styPanel.Width(width).Render(s.logs.View()))
	}
	return strings.TrimRight(b.String(), "\n")
}

func serviceWord(rep serve.StatusReport) string {
	switch {
	case rep.Ready:
		return styLive.Render("● ") + styStrong.Render("running and answering")
	case rep.Active:
		return styAmber.Render("● ") + styStrong.Render("running, endpoint not answering yet")
	case rep.Installed:
		return styDim.Render("○ ") + styStrong.Render("installed, not running")
	default:
		return styDim.Render("○ ") + styStrong.Render("not installed")
	}
}

func endpointWord(rep serve.StatusReport) string {
	if rep.EndpointReady {
		return fmt.Sprintf("answering (%d)", rep.EndpointCode)
	}
	if rep.EndpointCode == 0 {
		return "no answer"
	}
	return fmt.Sprintf("answered %d", rep.EndpointCode)
}
