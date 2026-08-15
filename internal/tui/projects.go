package tui

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/projects"
	"github.com/wzslr321/torio/internal/redact"
	"github.com/wzslr321/torio/internal/serve"
)

type projectsMsg struct {
	list []projects.Project
	err  error
}

// showMsg is one project's detail arriving for the detail panel.
type showMsg struct {
	report projects.ShowReport
	err    error
}

// connectMsg is the gateway's state arriving for the connect panel.
type connectMsg struct {
	report serve.StatusReport
	err    error
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

	// editing is the remote correction, open on one project at a time. It
	// reuses fields because it is a form like the addition, and keeps the id
	// separately because the correction names the record it rewrites rather
	// than whatever the cursor is on when it is submitted.
	editing bool
	editID  string

	// showID is the project the detail panel is open for, empty when closed,
	// with showRep and showErr the answer it is waiting on.
	showID     string
	showRep    projects.ShowReport
	showErr    string
	showLoaded bool

	// connectID is the project the gateway panel is open for, empty when
	// closed. The panel is the Enter answer on a backend whose way into a
	// project is a service rather than a session: what the gateway is doing,
	// and the tunnel that reaches it.
	connectID     string
	connectRep    serve.StatusReport
	connectErr    string
	connectLoaded bool
}

// capturing is true while the screen owns the keyboard, so the root does not
// read a typed "q" as a request to quit.
func (s *projectsScreen) capturing() bool { return s.adding || s.editing }

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

// openEdit opens the remote correction on one project, prefilled with what the
// record holds: a correction is an edit of an address, not a retyping of one,
// and retyping is how a second repository ends up under a name that already
// means something.
func (s *projectsScreen) openEdit(p projects.Project) {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 400
	ti.Width = 52
	ti.SetValue(p.Remote)
	ti.CursorEnd()
	ti.Focus()
	s.fields = []textinput.Model{ti}
	s.focus = 0
	s.editID = p.ID
	s.editing = true
}

func (s *projectsScreen) closeEdit() {
	s.editing = false
	s.editID = ""
	s.fields = nil
}

// updateEdit drives the correction form.
func (s *projectsScreen) updateEdit(r *root, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		s.closeEdit()
		return nil
	case "enter":
		remote := strings.TrimSpace(s.fields[0].Value())
		id := s.editID
		if remote == "" {
			r.errText = "a project needs a remote"
			return nil
		}
		s.closeEdit()
		d := r.deps
		return r.run("correcting the remote of "+id, true, func(ctx context.Context) error {
			return d.ProjectSetRemote(ctx, id, remote)
		})
	}
	var cmd tea.Cmd
	s.fields[s.focus], cmd = s.fields[s.focus].Update(msg)
	return cmd
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

	case showMsg:
		s.showLoaded = true
		s.showRep = msg.report
		s.showErr = ""
		if msg.err != nil {
			s.showErr = redact.String(msg.err.Error())
		}
		return nil

	case connectMsg:
		s.connectLoaded = true
		s.connectRep = msg.report
		s.connectErr = ""
		if msg.err != nil {
			s.connectErr = redact.String(msg.err.Error())
		}
		return nil

	case tea.KeyMsg:
		if s.adding {
			return s.updateForm(r, msg)
		}
		if s.editing {
			return s.updateEdit(r, msg)
		}
		if s.showID != "" {
			if k := msg.String(); k == "esc" || k == "enter" || k == "q" {
				s.showID = ""
			}
			return nil
		}
		if s.connectID != "" {
			return s.updateConnect(msg)
		}
		if s.confirm {
			return s.updateConfirm(r, msg)
		}
		return s.updateList(r, msg)
	}
	return nil
}

func (s *projectsScreen) updateConnect(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "enter":
		s.connectID = ""
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
		return r.runDetailed("adding "+id, true, func(ctx context.Context) (string, error) {
			key, err := d.ProjectAdd(ctx, id, remote)
			if err != nil && key != nil {
				return deployKeyDetail(key), err
			}
			return "", err
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
	case "v":
		p, ok := s.selected()
		if !ok || d.ProjectShow == nil {
			return nil
		}
		return s.openShow(r, p)
	case "e":
		p, ok := s.selected()
		if !ok || d.ProjectSetRemote == nil {
			return nil
		}
		s.openEdit(p)
		return textinput.Blink
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
		if d.AgentSpec != nil {
			return s.openSession(r, p, "agent session in "+p.ID, d.AgentSpec)
		}
		// No session declared. When the backend runs a service instead, its
		// way into a project is the gateway, and Enter owes the operator that
		// way rather than a failure about the way it does not have.
		if d.ServiceDeclared && d.ServeStatus != nil {
			return s.openConnect(r, p)
		}
		r.errText = "this backend opens no agent session in a checkout"
		return nil
	case "s":
		p, ok := s.selected()
		if !ok {
			return nil
		}
		if d.ShellSpec == nil {
			r.errText = "this backend opens no shell in a checkout"
			return nil
		}
		return s.openSession(r, p, "shell in "+p.ID, d.ShellSpec)
	}
	return nil
}

// openSession resolves one project session and hands the terminal to it.
//
// The resolution runs the same preflight the command surface runs, so it can
// refuse. One refusal is answerable here: a project the registry holds and this
// guest has no checkout for is the ordinary state of a project the operator has
// not opened on this backend yet, and the record already says where to get it.
// The hub makes it, says so while it does, and resolves once more (ADR-0024).
//
// Every other refusal stands. A checkout that exists and disagrees with the
// record is a working tree, and cloning over one is the destructive act Torio
// refuses everywhere else.
func (s *projectsScreen) openSession(r *root, p projects.Project, label string, spec func(context.Context, string) (execx.InteractiveCommand, error)) tea.Cmd {
	return r.handoff(label, func(ctx context.Context) (execx.InteractiveCommand, error) {
		cmd, err := spec(ctx, p.ID)
		if err == nil || !projects.IsCheckoutAbsentOnly(err) || r.deps.ProjectMaterialize == nil {
			return cmd, err
		}
		if key, mErr := r.deps.ProjectMaterialize(ctx, p.ID); mErr != nil {
			return execx.InteractiveCommand{}, &materializeError{err: mErr, key: key}
		}
		// Exactly one retry. A second refusal is a refusal, and a loop that
		// kept answering it would clone in circles.
		return spec(ctx, p.ID)
	})
}

// materializeError carries a failed materialization and the deploy key it left
// for the operator to authorize, so the banner can show what makes the failure
// actionable rather than only naming it.
type materializeError struct {
	err error
	key *projects.DeployKey
}

func (e *materializeError) Error() string { return e.err.Error() }
func (e *materializeError) Unwrap() error { return e.err }

// openShow opens the detail panel and asks what the guest holds. The ask is a
// read, so it takes no busy lock: the panel says it is asking until the answer
// lands, exactly as the gateway panel does.
func (s *projectsScreen) openShow(r *root, p projects.Project) tea.Cmd {
	s.showID = p.ID
	s.showLoaded = false
	s.showErr = ""
	d := r.deps
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(d.parentContext(), longOr(d.Timeout))
		defer cancel()
		rep, err := d.ProjectShow(ctx, p.ID)
		return showMsg{report: rep, err: err}
	}
}

// openConnect opens the gateway panel for one project and asks the service
// how it is doing. The ask is a read, not an operation: it takes no busy lock,
// and the panel says it is asking until the answer lands.
func (s *projectsScreen) openConnect(r *root, p projects.Project) tea.Cmd {
	s.connectID = p.ID
	s.connectLoaded = false
	s.connectErr = ""
	d := r.deps
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(d.parentContext(), longOr(d.Timeout))
		defer cancel()
		rep, err := d.ServeStatus(ctx)
		return connectMsg{report: rep, err: err}
	}
}

// keys names only what the keys do on this backend. A hub bound to a backend
// with no session must not offer "enter agent" and "s shell", and one bound to
// a backend that keeps no registry must not offer "u use": each would be a key
// that does nothing, and the footer is the one place the hub says which keys
// are live.
func (s *projectsScreen) keys(r *root) string {
	switch {
	case s.adding:
		return "enter add · tab field · esc cancel"
	case s.editing:
		return "enter save · esc cancel"
	case s.connectID != "":
		return "esc close"
	case s.confirm:
		return "y remove · n keep"
	}
	if s.showID != "" {
		return "esc close"
	}
	parts := []string{"a add"}
	if r.deps.ProjectShow != nil {
		parts = append(parts, "v show")
	}
	if r.deps.ProjectSetRemote != nil {
		parts = append(parts, "e remote")
	}
	if r.deps.ProjectUse != nil {
		parts = append(parts, "u use")
	}
	if r.deps.AgentSpec == nil {
		parts = append(parts, "enter open")
	} else {
		parts = append(parts, "enter agent", "s shell")
	}
	return strings.Join(append(parts, "d remove"), " · ")
}

func (s *projectsScreen) view(r *root, w int) string {
	var b strings.Builder

	if s.showID != "" {
		return s.viewShow()
	}
	if s.connectID != "" {
		return s.viewConnect(r)
	}

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

	if s.editing {
		b.WriteString(styStrong.Render("Correct the remote of "+s.editID) + "\n")
		b.WriteString(styMuted.Render(
			"The registry is shared, so this applies to every backend. A host only this "+
				"machine knows resolves nowhere else.") + "\n\n")
		b.WriteString(styWorking.Render("▸ ") + styMuted.Render(pad("remote", 8)) + styField.Render(s.fields[0].View()) + "\n")
		b.WriteString("\n" + styBtn.Render("Save"))
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

// viewConnect is the way forward into a project on a backend whose surface is
// a gateway rather than a terminal session. It says what the runbooks say
// (docs/content/blocks/tunnel.md, desktop-connect.md): the state the service
// is in, the forward the operator opens, and where Hermes Desktop points. The
// command lines are rendered unstyled so a terminal selection picks up no
// decoration.
// viewShow renders what the guest holds for one project. It names markers and
// counts, never a file inside the checkout: `project show` returns no file
// names, no diffs and no raw Git output, and the hub is not a second answer.
func (s *projectsScreen) viewShow() string {
	var b strings.Builder
	b.WriteString(styStrong.Render(s.showID) + "\n\n")
	switch {
	case s.showErr != "":
		b.WriteString(styAmber.Render("could not be read: ") + styText.Render(s.showErr) + "\n")
		return b.String()
	case !s.showLoaded:
		b.WriteString(styMuted.Render("Asking the guest…") + "\n")
		return b.String()
	}

	rep := s.showRep
	c := rep.Checkout
	if rep.Project.Remote != "" {
		b.WriteString(styMuted.Render(rep.Project.Remote) + "\n")
	}
	if rep.Project.Path != "" {
		b.WriteString(styMuted.Render(rep.Project.Path) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(line(c.PathExists, "checkout", ternary(c.PathExists, "present", "absent")))
	b.WriteString(line(c.Repository, "git repository", ternary(c.Repository, "yes", "no")))
	b.WriteString(line(c.OriginMatches, "origin matches the record", ternary(c.OriginMatches, "yes", "no")))
	b.WriteString(line(c.Clean, "worktree", ternary(c.Clean, "clean", "has uncommitted work")))
	b.WriteString(line(c.SharedPermissions, "shared access", ternary(c.SharedPermissions, "yes", "no")))

	if len(rep.Issues) == 0 {
		b.WriteString("\n" + styMuted.Render("no issues") + "\n")
		return b.String()
	}
	b.WriteString("\n" + styAmber.Render("issues") + "\n")
	for _, issue := range rep.Issues {
		b.WriteString(styMuted.Render("  "+issue) + "\n")
	}
	return b.String()
}

func (s *projectsScreen) viewConnect(r *root) string {
	d := r.deps
	var b strings.Builder
	b.WriteString(styStrong.Render("Continue with "+s.connectID+" on "+d.Backend) + "\n")
	b.WriteString(styMuted.Render("This backend serves a gateway rather than a terminal session. The guest binds") + "\n")
	b.WriteString(styMuted.Render("loopback only, so clients reach it over an SSH tunnel you control.") + "\n\n")

	if !s.connectLoaded {
		return b.String() + styMuted.Render("Asking the gateway how it is doing…")
	}

	rep := s.connectRep
	b.WriteString(serviceWord(rep) + "\n")
	if !rep.Ready {
		if s.connectErr != "" {
			b.WriteString(styMuted.Render(s.connectErr) + "\n")
		}
		b.WriteString(styMuted.Render("Start it from the Serve tab (5), then come back here.") + "\n")
	}

	if guest, host, ok := gatewayPorts(rep.URL); ok {
		b.WriteString("\n" + styMuted.Render("Forward a host port to it:") + "\n\n")
		b.WriteString("  ssh -F ~/.lima/" + d.Instance + "/ssh.config -L " + host + ":127.0.0.1:" + guest + " -N -f \\\n")
		b.WriteString("      -o ExitOnForwardFailure=yes lima-" + d.Instance + "\n\n")
		b.WriteString(styMuted.Render("Then in Hermes Desktop, Settings, Gateway Connection, Remote gateway: set the") + "\n")
		b.WriteString(styMuted.Render("Remote URL to ") + styText.Render("http://127.0.0.1:"+host) + styMuted.Render(" with the session token you pinned.") + "\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// gatewayPorts derives the two ends of the operator's forward from the guest
// loopback URL the status report probed. The host port is the guest port moved
// up by ten thousand, the convention the runbooks teach (9119 becomes 19119),
// so the hub and the docs describe the same tunnel.
func gatewayPorts(raw string) (guest, host string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Port() == "" {
		return "", "", false
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		return "", "", false
	}
	return u.Port(), strconv.Itoa(p + 10000), true
}

// deployKeyDetail is the operator's way forward when an add fails with a key
// the guest holds. It says what the command surface says (printDeployKey in
// internal/cli): the key is on its own line so a terminal selection picks up
// no prose, and the account-key warning sits next to the key because the
// screen showing the key is where that choice gets made.
func deployKeyDetail(key *projects.DeployKey) string {
	state := "The guest already held a deploy key for this project."
	if key.Generated {
		state = "The guest generated a deploy key for this project."
	}
	return state + " Torio holds no copy of its private half.\n\n" +
		key.PublicKey + "\n\n" +
		"Add that key to the repository on " + key.Host + " as a deploy key, with write access off,\n" +
		"then run the add again. Adding it to your account instead would give the guest\n" +
		"write access to every repository that account can reach."
}
