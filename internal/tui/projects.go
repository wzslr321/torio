package tui

import (
	"context"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/projects"
	"github.com/wzslr321/torio/internal/redact"
)

type projectsMsg struct {
	list []projects.Project
	err  error
}

// projectsScreen is the registry and the ways into a checkout.
type projectsScreen struct {
	list   []projects.Project
	loaded bool
	failed string
	cursor int

	adding  bool
	focus   int
	fields  []textinput.Model
	confirm bool
}

func newProjectsScreen() projectsScreen { return projectsScreen{} }

// capturing is true while the screen owns the keyboard, so the root does not
// read a typed "q" as a request to quit.
func (s *projectsScreen) capturing() bool { return s.adding }

func (s *projectsScreen) load(d Deps) tea.Cmd {
	if d.ProjectList == nil {
		return nil
	}
	list := d.ProjectList
	return func() tea.Msg {
		out, err := list()
		return projectsMsg{list: out, err: err}
	}
}

func (s *projectsScreen) openForm() {
	s.fields = make([]textinput.Model, 2)
	for i, ph := range []string{"my-app", "git@github.com:you/my-app.git"} {
		ti := textinput.New()
		ti.Placeholder = ph
		ti.Prompt = ""
		ti.CharLimit = 200
		ti.Width = 40
		s.fields[i] = ti
	}
	s.focus = 0
	s.fields[0].Focus()
	s.adding = true
}

func (s *projectsScreen) closeForm() {
	s.adding = false
	s.fields = nil
}

func (s *projectsScreen) selected() (projects.Project, bool) {
	if s.cursor < 0 || s.cursor >= len(s.list) {
		return projects.Project{}, false
	}
	return s.list[s.cursor], true
}

func (s *projectsScreen) update(r *root, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case projectsMsg:
		s.loaded = true
		if msg.err != nil {
			s.failed = redact.String(msg.err.Error())
			return nil
		}
		s.failed = ""
		s.list = msg.list
		if s.cursor >= len(s.list) {
			s.cursor = max(0, len(s.list)-1)
		}
		return nil

	case tea.KeyMsg:
		if s.adding {
			return s.updateForm(r, msg)
		}
		if s.confirm {
			return s.updateConfirm(r, msg)
		}
		return s.updateList(r, msg)
	}
	return nil
}

func (s *projectsScreen) updateForm(r *root, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		s.closeForm()
		return nil
	case "tab", "down":
		s.fields[s.focus].Blur()
		s.focus = (s.focus + 1) % len(s.fields)
		s.fields[s.focus].Focus()
		return nil
	case "shift+tab", "up":
		s.fields[s.focus].Blur()
		s.focus = (s.focus - 1 + len(s.fields)) % len(s.fields)
		s.fields[s.focus].Focus()
		return nil
	case "enter":
		id := strings.TrimSpace(s.fields[0].Value())
		remote := strings.TrimSpace(s.fields[1].Value())
		if id == "" {
			r.errText = "a project needs an id"
			return nil
		}
		s.closeForm()
		d := r.deps
		return r.run("adding "+id, true, func(ctx context.Context) error {
			return d.ProjectAdd(ctx, id, remote)
		})
	}
	var cmd tea.Cmd
	s.fields[s.focus], cmd = s.fields[s.focus].Update(msg)
	return cmd
}

func (s *projectsScreen) updateConfirm(r *root, msg tea.KeyMsg) tea.Cmd {
	p, ok := s.selected()
	switch msg.String() {
	case "y":
		s.confirm = false
		if !ok {
			return nil
		}
		d := r.deps
		return r.run("removing "+p.ID, false, func(ctx context.Context) error {
			return d.ProjectRemove(ctx, p.ID)
		})
	case "n", "esc":
		s.confirm = false
	}
	return nil
}

func (s *projectsScreen) updateList(r *root, msg tea.KeyMsg) tea.Cmd {
	d := r.deps
	switch msg.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < len(s.list)-1 {
			s.cursor++
		}
	case "a":
		s.openForm()
		return textinput.Blink
	case "d":
		if _, ok := s.selected(); ok {
			s.confirm = true
		}
	case "u":
		p, ok := s.selected()
		if !ok || d.ProjectUse == nil {
			return nil
		}
		return r.run("selecting "+p.ID, false, func(ctx context.Context) error {
			return d.ProjectUse(ctx, p.ID)
		})
	case "enter":
		p, ok := s.selected()
		if !ok {
			return nil
		}
		if d.AgentSpec == nil {
			r.errText = "this backend opens no agent session in a checkout"
			return nil
		}
		return r.handoff("agent session in "+p.ID, func() (execx.InteractiveCommand, error) {
			return d.AgentSpec(p.Path)
		})
	case "s":
		p, ok := s.selected()
		if !ok {
			return nil
		}
		if d.ShellSpec == nil {
			r.errText = "this backend opens no shell in a checkout"
			return nil
		}
		return r.handoff("shell in "+p.ID, func() (execx.InteractiveCommand, error) {
			return d.ShellSpec(p.Path)
		})
	}
	return nil
}

func (s *projectsScreen) keys() string {
	switch {
	case s.adding:
		return "enter add · tab field · esc cancel"
	case s.confirm:
		return "y remove · n keep"
	default:
		return "a add · u use · enter agent · s shell · d remove"
	}
}

func (s *projectsScreen) view(r *root, w int) string {
	var b strings.Builder

	if s.adding {
		b.WriteString(styStrong.Render("Add a project") + "\n")
		b.WriteString(styMuted.Render("The remote is optional for a project this backend already knows.") + "\n\n")
		for i, label := range []string{"id", "remote"} {
			marker := "  "
			if i == s.focus {
				marker = styWorking.Render("▸ ")
			}
			b.WriteString(marker + styMuted.Render(pad(label, 8)) + styField.Render(s.fields[i].View()) + "\n")
		}
		b.WriteString("\n" + styBtn.Render("Add"))
		return b.String()
	}

	switch {
	case s.failed != "":
		return styAmber.Render("the registry could not be read: ") + styText.Render(s.failed)
	case !s.loaded:
		return styMuted.Render("Reading the project registry…")
	case len(s.list) == 0:
		return styMuted.Render("No projects registered yet.") + "\n\n" + styDim.Render("press a to add one")
	}

	b.WriteString(styMuted.Render(plural(len(s.list), "project", "projects")+" on "+r.deps.Instance) + "\n\n")
	for i, p := range s.list {
		marker := "  "
		// An id is allowed to be far longer than the column, so it is cut to
		// fit rather than padded to nothing: a name that filled the column
		// exactly would run straight into the remote with no gap, and the two
		// would read as one string.
		id := pad(truncate(p.ID, idColumn-1), idColumn)
		name := styText.Render(id)
		if i == s.cursor {
			marker = styWorking.Render("▸ ")
			name = styStrong.Render(id)
		}
		remote := p.Remote
		if remote == "" {
			remote = "(no remote)"
		}
		b.WriteString(marker + name + styMuted.Render(truncate(remote, maxRemote(w))) + "\n")
	}

	if s.confirm {
		if p, ok := s.selected(); ok {
			b.WriteString("\n" + styErrPanel.Render(
				styText.Render("Remove "+p.ID+" from the registry? ")+styMuted.Render("The checkout on the guest is left alone.")+"\n"+
					styDim.Render("y remove · n keep")))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// idColumn is the width the project id column occupies, including the gap that
// separates it from the remote.
const idColumn = 22

func maxRemote(w int) int {
	n := w - idColumn - 4
	if n < 20 {
		return 20
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
