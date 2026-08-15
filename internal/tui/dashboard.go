package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/redact"
	"github.com/wzslr321/torio/internal/status"
	"github.com/wzslr321/torio/internal/tui/wizard"
)

type pollMsg struct {
	report status.Report
	err    error
}

// dashScreen renders the cross-box poll. It shows every box on the host, not
// only the one this invocation selected: which box needs a human is exactly the
// question an operator opens the hub to answer, and a surface that showed one
// box would answer it wrongly by omission.
type dashScreen struct {
	report status.Report
	loaded bool
	failed string
	cursor int
	// confirmStop is the guard in front of stopping this box.
	confirmStop bool

	// The status-line panel: first the surface picker, then the recipe
	// `status setup` prints for the surface picked. The hub shows the text and
	// writes nothing — the dotfile it belongs in is the operator's.
	recipePick    bool
	recipeCursor  int
	recipeSurface string
	recipe        string
}

func (s *dashScreen) load(d Deps) tea.Cmd {
	if d.Poll == nil {
		return nil
	}
	timeout := d.Timeout
	poll := d.Poll
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(d.parentContext(), longOr(timeout))
		defer cancel()
		rep, err := poll(ctx)
		return pollMsg{report: rep, err: err}
	}
}

func (s *dashScreen) update(r *root, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case pollMsg:
		s.loaded = true
		if msg.err != nil {
			s.failed = redact.String(msg.err.Error())
			return nil
		}
		s.failed = ""
		s.report = msg.report
		if s.cursor >= len(s.report.Instances) {
			s.cursor = 0
		}
		return nil
	case tea.KeyMsg:
		if s.confirmStop {
			switch msg.String() {
			case "y":
				s.confirmStop = false
				d := r.deps
				return r.run("stopping "+r.deps.Instance, true, d.VMStop)
			case "n", "esc":
				s.confirmStop = false
			}
			return nil
		}
		if s.recipe != "" {
			if msg.String() == "esc" {
				s.recipe, s.recipeSurface = "", ""
			}
			return nil
		}
		if s.recipePick {
			switch msg.String() {
			case "esc":
				s.recipePick = false
			case "up", "k":
				if s.recipeCursor > 0 {
					s.recipeCursor--
				}
			case "down", "j":
				if s.recipeCursor < len(r.deps.StatusSurfaces)-1 {
					s.recipeCursor++
				}
			case "enter":
				s.recipePick = false
				surface := r.deps.StatusSurfaces[s.recipeCursor]
				// Synchronous on purpose: the recipe is composed text, not a
				// probe, so there is nothing to wait on and no spinner to hold.
				text, err := r.deps.StatusSetup(surface)
				if err != nil {
					r.errText = redact.String(err.Error())
					return nil
				}
				s.recipeSurface, s.recipe = surface, text
			}
			return nil
		}
		switch msg.String() {
		case "t":
			// The recipe that puts the ambient status line on a surface, shown
			// and never written: the dotfile it belongs in is the operator's.
			if r.deps.StatusSetup != nil && len(r.deps.StatusSurfaces) > 0 {
				s.recipePick = true
				s.recipeCursor = 0
			}
		case "x":
			// Only a running box has anything to stop, and stopping one takes
			// the agent sessions it is carrying with it, so it is asked for
			// rather than done on a keypress.
			if r.deps.VMStop != nil && r.facts.Box == lima.StateRunning {
				s.confirmStop = true
			}
		case "s":
			// The login identity's own shell inside the bound box, the same
			// session `torio vm shell` opens. Only a running box has one.
			if r.deps.VMShellSpec != nil && r.facts.Box == lima.StateRunning {
				return r.handoff("shell into "+r.deps.Instance, func(context.Context) (execx.InteractiveCommand, error) {
					return r.deps.VMShellSpec()
				})
			}
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < len(s.report.Instances)-1 {
				s.cursor++
			}
		}
	}
	return nil
}

func (s *dashScreen) keys(r *root) string {
	if s.confirmStop {
		return "y stop · n keep"
	}
	if s.recipe != "" {
		return "esc close"
	}
	if s.recipePick {
		return "↑/↓ pick · enter show · esc close"
	}
	parts := []string{"↑/↓ box", "w wizard"}
	if r.facts.Box == lima.StateRunning {
		if r.deps.VMShellSpec != nil {
			parts = append(parts, "s shell")
		}
		if r.deps.VMStop != nil {
			parts = append(parts, "x stop")
		}
	}
	if r.deps.StatusSetup != nil && len(r.deps.StatusSurfaces) > 0 {
		parts = append(parts, "t status line")
	}
	return strings.Join(parts, " · ")
}

func (s *dashScreen) view(r *root, w int) string {
	if s.recipe != "" {
		// The recipe body is rendered unstyled so a terminal selection of it
		// picks up no decoration; its own comment lines name the file it
		// belongs in and the command that reloads it.
		return styStrong.Render("Status line on "+s.recipeSurface) + "  " +
			styMuted.Render("same as: torio status setup "+s.recipeSurface) +
			"\n\n" + s.recipe
	}
	if s.recipePick {
		var b strings.Builder
		b.WriteString(styStrong.Render("Put the status line on a surface") + "\n")
		b.WriteString(styMuted.Render("The recipe is shown, never written; the dotfile is yours.") + "\n\n")
		for i, surface := range r.deps.StatusSurfaces {
			if i == s.recipeCursor {
				b.WriteString(styWorking.Render("▸ ") + styText.Render(surface) + "\n")
				continue
			}
			b.WriteString("  " + styText.Render(surface) + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}

	var b strings.Builder

	if banner := attentionBanner(s.report); banner != "" {
		b.WriteString(banner + "  ")
	}
	b.WriteString(nextHint(r) + "\n\n")

	switch {
	case s.failed != "":
		b.WriteString(styAmber.Render("the poll failed: ") + styText.Render(s.failed))
		return b.String()
	case !s.loaded:
		b.WriteString(styMuted.Render("Polling every box on this host…"))
		return b.String()
	case len(s.report.Instances) == 0:
		b.WriteString(styMuted.Render("No boxes on this host yet. The Setup screen creates one."))
		return b.String()
	}

	b.WriteString(styMuted.Render(row("INSTANCE", "BOX", "BACKEND", "SESSIONS", "WAITING", "PROGRESS")) + "\n")
	for i, in := range s.report.Instances {
		line := row(
			truncate(in.Name, 18),
			boxWord(in.Box),
			backendWord(in.Backend),
			sessionWord(in.Session),
			waitingWord(in.Waiting),
			progressWord(in.Progress),
		)
		if i == s.cursor {
			b.WriteString(styWorking.Render("▸") + line + "\n")
			continue
		}
		b.WriteString(" " + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// row lays the columns out at fixed widths. A tab would put the alignment in
// the terminal's hands, and the row an operator most needs to compare with the
// one above it is the one whose name is longest.
func row(instance, box, backend, sessions, waiting, progress string) string {
	return fmt.Sprintf("%s%s%s%s%s%s",
		pad(instance, 20), pad(box, 12), pad(backend, 14),
		pad(sessions, 11), pad(waiting, 19), progress)
}

// attentionBanner is the one inverted element in the hub. It appears only when
// a backend is provably blocked on the operator.
func attentionBanner(rep status.Report) string {
	for _, in := range rep.Instances {
		if in.Waiting.State == status.Known && in.Waiting.Waiting {
			return styAttention.Render(fmt.Sprintf("%s needs you %d", in.Name, len(in.Waiting.Waits)))
		}
	}
	return ""
}

// nextHint is the dashboard's guidance, and it is the wizard's answer verbatim.
// Two surfaces deriving "what now" separately is how they come to disagree.
func nextHint(r *root) string {
	if !r.verified {
		return styDim.Render("reading " + r.deps.Instance + "…")
	}
	step := wizard.Next(r.facts)
	if step == wizard.StepDone {
		return styMuted.Render("next: ") + styLive.Render("nothing outstanding on "+r.deps.Instance)
	}
	return styMuted.Render("next: ") +
		styText.Render(wizard.Describe(step).Title) +
		styDim.Render("  press w")
}

func boxWord(box string) string {
	switch box {
	case "running":
		return styLive.Render("● running")
	case "stopped":
		return styDim.Render("○ stopped")
	default:
		return styAmber.Render("? " + box)
	}
}

func backendWord(f status.BackendField) string {
	if f.State != status.Known {
		return styAmber.Render("?")
	}
	return styText.Render(f.Name)
}

func sessionWord(f status.SessionField) string {
	switch f.State {
	case status.NotApplicable:
		return styDim.Render("—")
	case status.Known:
		if len(f.Sessions) == 0 {
			return styDim.Render("0")
		}
		return styText.Render(fmt.Sprintf("%d", len(f.Sessions)))
	default:
		return styAmber.Render("?")
	}
}

func waitingWord(f status.WaitingField) string {
	switch f.State {
	case status.NotApplicable:
		return styDim.Render("—")
	case status.Known:
		if !f.Waiting {
			return styDim.Render("—")
		}
		return styAmber.Render(fmt.Sprintf("needs you %d", len(f.Waits)))
	default:
		return styAmber.Render("?")
	}
}

func progressWord(f status.ProgressField) string {
	switch f.State {
	case status.NotApplicable:
		return styDim.Render("—")
	case status.Known:
		return styWorking.Render("· " + compactAge(f.AgeSeconds))
	default:
		return styAmber.Render("?")
	}
}

// compactAge renders an age the way the status line already does, so the two
// surfaces read the same.
func compactAge(seconds int64) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh", seconds/3600)
	default:
		return fmt.Sprintf("%dd", seconds/86400)
	}
}
