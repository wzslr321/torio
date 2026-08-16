package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/tui/wizard"
)

// The defaults `torio vm init` uses. They are repeated here as the form's
// starting values so an operator who has no opinion can press enter, and one
// who does can see what they are changing from.
const (
	defaultCPUs   = "4"
	defaultMemory = "8GiB"
	defaultDisk   = "60GiB"
)

// setupScreen walks the operator through whatever setup is still missing. It
// holds no idea of its own about the order: wizard.Next answers that from the
// facts the root probed.
type setupScreen struct {
	editing bool
	focus   int
	fields  []textinput.Model
}

func (s *setupScreen) capturing() bool { return s.editing }

func (s *setupScreen) openForm() {
	labels := []string{defaultCPUs, defaultMemory, defaultDisk}
	s.fields = make([]textinput.Model, len(labels))
	for i, v := range labels {
		ti := textinput.New()
		ti.SetValue(v)
		ti.CharLimit = 16
		ti.Width = 12
		ti.Prompt = ""
		s.fields[i] = ti
	}
	s.focus = 0
	s.fields[0].Focus()
	s.editing = true
}

func (s *setupScreen) closeForm() {
	s.editing = false
	s.fields = nil
}

func (s *setupScreen) update(r *root, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if s.editing {
		switch key.String() {
		case "esc":
			s.closeForm()
			return nil
		case "tab", "down":
			s.focusNext(1)
			return nil
		case "shift+tab", "up":
			s.focusNext(-1)
			return nil
		case "enter":
			opts, err := s.readForm()
			if err != nil {
				r.errText = err.Error()
				return nil
			}
			s.closeForm()
			d := r.deps
			return r.run("creating the box", true, func(ctx context.Context) error {
				return d.VMInit(ctx, opts)
			})
		}
		var cmd tea.Cmd
		s.fields[s.focus], cmd = s.fields[s.focus].Update(msg)
		return cmd
	}

	if key.String() == "enter" {
		return s.act(r)
	}
	return nil
}

func (s *setupScreen) focusNext(delta int) {
	s.fields[s.focus].Blur()
	s.focus = (s.focus + delta + len(s.fields)) % len(s.fields)
	s.fields[s.focus].Focus()
}

func (s *setupScreen) readForm() (VMInitOptions, error) {
	cpus, err := strconv.Atoi(strings.TrimSpace(s.fields[0].Value()))
	if err != nil || cpus < 1 {
		return VMInitOptions{}, fmt.Errorf("cpus must be a whole number of at least 1")
	}
	memory := strings.TrimSpace(s.fields[1].Value())
	disk := strings.TrimSpace(s.fields[2].Value())
	if memory == "" || disk == "" {
		return VMInitOptions{}, fmt.Errorf("memory and disk must be set")
	}
	return VMInitOptions{CPUs: cpus, Memory: memory, Disk: disk}, nil
}

// act performs the current step. Each branch is the same manager call the
// equivalent command makes.
func (s *setupScreen) act(r *root) tea.Cmd {
	// The same rule the view applies: a step derived from facts nothing has
	// proven is not a step. On a running box the unproven answer is bootstrap,
	// which is minutes of guest work nobody asked for.
	if !r.verified {
		return nil
	}
	d := r.deps
	switch wizard.Next(r.facts) {
	case wizard.StepVMInit:
		s.openForm()
		return textinput.Blink

	case wizard.StepVMStart:
		return r.run("starting the box", false, d.VMStart)

	case wizard.StepBootstrap:
		return r.run("bootstrapping the guest", true, func(ctx context.Context) error {
			_, err := d.Bootstrap(ctx, false)
			return err
		})

	case wizard.StepBackendLogin:
		// The login argv is the backend's own constant and reaches nothing to
		// build, so it takes the context without using it.
		return r.handoff("backend login", func(context.Context) (execx.InteractiveCommand, error) {
			return d.LoginSpec()
		})

	case wizard.StepBrainInit:
		return r.run("creating the Second Brain", true, d.BrainInit)

	case wizard.StepProjectAdd:
		// The projects screen already owns the form and the registry actions.
		// Reimplementing them here would be a second place to keep correct.
		r.switchTo(screenProjects)
		r.projects.openForm()
		return tea.Batch(r.projects.load(d), textinput.Blink)
	}
	return nil
}

func (s *setupScreen) keys(r *root) string {
	if s.editing {
		return "enter create · tab field · esc cancel"
	}
	if !r.verified {
		return ""
	}
	switch wizard.Next(r.facts) {
	case wizard.StepDone, wizard.StepBoxUnusable:
		return ""
	default:
		return "enter run this step"
	}
}

func (s *setupScreen) view(r *root, w int) string {
	switch {
	case !r.probed:
		return styMuted.Render("Reading the box…")
	case !r.verified:
		// The box is known and the guest is still being asked. Naming a step
		// now would name the one that follows from facts nothing has proven.
		return styMuted.Render("Checking what the guest already holds. This takes a few seconds.")
	}

	rail := s.rail(r)
	detail := s.detail(r, w)

	// Two columns while there is room for both, stacked when there is not.
	if w >= 76 {
		return joinColumns(rail, detail, 26)
	}
	return rail + "\n\n" + detail
}

func (s *setupScreen) rail(r *root) string {
	current := wizard.Next(r.facts)
	var b strings.Builder
	b.WriteString(styMuted.Render("Setup") + "\n\n")
	for _, st := range wizard.Plan(r.facts) {
		title := wizard.Describe(st.Step).Title
		switch st.State {
		case wizard.StageDone:
			b.WriteString(styLive.Render("✓ ") + styMuted.Render(title))
		case wizard.StageCurrent:
			b.WriteString(styWorking.Render("▸ ") + styStrong.Render(title))
		default:
			b.WriteString(styDim.Render("  " + title))
		}
		b.WriteString("\n")
	}
	if current == wizard.StepDone {
		b.WriteString("\n" + styLive.Render("all steps complete"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *setupScreen) detail(r *root, w int) string {
	step := wizard.Next(r.facts)
	d := wizard.Describe(step)

	var b strings.Builder
	b.WriteString(styStrong.Render(d.Title) + "\n")
	b.WriteString(styMuted.Render(wrap(d.Detail, detailWidth(w))) + "\n")
	if d.Command != "" {
		b.WriteString("\n" + styDim.Render("same as: "+d.Command) + "\n")
	}

	if s.editing {
		b.WriteString("\n")
		for i, label := range []string{"cpus", "memory", "disk"} {
			marker := "  "
			if i == s.focus {
				marker = styWorking.Render("▸ ")
			}
			fmt.Fprintf(&b, "%s%s  %s\n",
				marker, styMuted.Render(pad(label, 7)), styField.Render(s.fields[i].View()))
		}
		b.WriteString("\n" + styBtn.Render("Create"))
		return b.String()
	}

	switch step {
	case wizard.StepDone:
		// Nothing is left for the operator to wire up by hand.
	case wizard.StepBoxUnusable:
		// No action offered on purpose: nothing the hub could run repairs it.
	default:
		b.WriteString("\n" + styBtn.Render(actionLabel(step)))
	}
	return b.String()
}

func actionLabel(step wizard.Step) string {
	switch step {
	case wizard.StepVMInit:
		return "Create"
	case wizard.StepVMStart:
		return "Start"
	case wizard.StepBootstrap:
		return "Bootstrap"
	case wizard.StepBackendLogin:
		return "Log in"
	case wizard.StepBrainInit:
		return "Create brain"
	case wizard.StepProjectAdd:
		return "Add project"
	default:
		return "Run"
	}
}

func detailWidth(w int) int {
	d := w - 32
	if d < 30 {
		d = 30
	}
	if d > 64 {
		d = 64
	}
	return d
}
