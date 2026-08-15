package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wzslr321/torio/internal/brain"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/redact"
	"github.com/wzslr321/torio/internal/tui/wizard"
)

type screenID int

const (
	screenSetup screenID = iota
	screenDashboard
	screenProjects
	screenBrain
	screenServe
)

var screenOrder = []screenID{screenSetup, screenDashboard, screenProjects, screenBrain, screenServe}

func (s screenID) title() string {
	switch s {
	case screenSetup:
		return "Setup"
	case screenDashboard:
		return "Dashboard"
	case screenProjects:
		return "Projects"
	case screenBrain:
		return "Brain"
	case screenServe:
		return "Serve"
	default:
		return "?"
	}
}

// boxMsg is the first half of a probe: what Lima says about the box. It costs
// one host command and arrives in milliseconds.
type boxMsg struct {
	state lima.State
	err   error
}

// factsMsg is the second half: what the guest could be proven to hold. It runs
// a verifying bootstrap and several guest commands, so on a running box it
// takes seconds. The two are separate messages so the header and the box state
// are on screen while the slow half is still running, rather than the operator
// watching an empty frame.
type factsMsg struct {
	facts wizard.Facts
	err   error
}

// opMsg is the completion of any action that either worked or did not. The
// label is what the operator was told is happening, so a failure names the
// thing that failed rather than the layer that reported it.
//
// detail is material the operator needs to act on the failure, rendered under
// the banner: today, the deploy key a failed add left the guest holding. A
// success discards it.
type opMsg struct {
	label  string
	err    error
	detail string
}

// specMsg carries a resolved interactive session, which the hub then hands the
// terminal to. Resolving the argv is ordinary work that can fail; handing over
// the terminal is not, so the two are separate steps.
type specMsg struct {
	label string
	spec  execx.InteractiveCommand
	err   error
}

// execDoneMsg is the operator coming back from an interactive session.
//
// detail is the end of what the session wrote to standard error. The hub
// repaints the whole screen on the way back, so a helper that refused and
// exited has already had its reason erased by the time the operator can read
// it; carrying the tail back is what keeps the reason on screen.
type execDoneMsg struct {
	err    error
	detail string
}

// rebindMsg is a finished rebind (ADR-0021): the seams of the new binding, or
// the error that left the old one in place.
type rebindMsg struct {
	name string
	deps Deps
	err  error
}

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type root struct {
	deps   Deps
	width  int
	height int

	active screenID

	// facts is what the last probe proved about this box. Both the wizard and
	// the dashboard's guidance read it, so one probe answers both.
	//
	// probed means the box state is known; verified means the guest half
	// finished too. Only the second one makes a setup step safe to name: an
	// unverified box has Bootstrapped false because nothing asked yet, and
	// showing the step that follows from it would tell an operator to bootstrap
	// a box that is already bootstrapped.
	facts     wizard.Facts
	probed    bool
	probing   bool
	verified  bool
	verifying bool

	// busy is the label of the single operation in flight, empty when idle.
	// One at a time is a deliberate limit: two concurrent guest operations on
	// one box would race over the same state, and the screens have nowhere
	// honest to show a second spinner.
	busy      string
	busyStart time.Time
	spin      spinner.Model

	// errText is the last failure, shown until dismissed.
	errText string
	// errDetail is what the failure left for the operator to act on, shown and
	// dismissed with errText. It is rendered unstyled so a terminal selection
	// of the key line picks up no decoration.
	errDetail string
	// note is a transient one-line outcome, replaced by the next one.
	note string

	// choosing is the rebind chooser (ADR-0021), open over the active screen.
	choosing     bool
	chooseCursor int

	setup    setupScreen
	dash     dashScreen
	projects projectsScreen
	brain    brainScreen
	serve    serveScreen
}

func newRoot(d Deps) *root {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styWorking

	r := &root{
		deps:   d,
		active: screenSetup,
		spin:   sp,
	}
	r.serve = newServeScreen()
	return r
}

func (r *root) Init() tea.Cmd {
	return tea.Batch(r.spin.Tick, r.probeFacts(), tick())
}

// opContext bounds one operation the way the command surface bounds one
// invocation. Long is for the operations that legitimately take minutes.
func (r *root) opContext(long bool) (context.Context, context.CancelFunc) {
	d := r.deps.Timeout
	if long {
		d = r.deps.LongTimeout
	}
	if d <= 0 {
		d = 30 * time.Second
	}
	return context.WithTimeout(r.deps.parentContext(), d)
}

// probeFacts asks the box what it is. It is the only place setup state is
// derived, and it re-runs after every action that could have changed it.
//
// It asks Lima first and the guest second. The box state is one host command
// and the guest half is many, so answering in two steps is what puts the header
// and the box on screen immediately instead of after the whole probe.
func (r *root) probeFacts() tea.Cmd {
	r.probing = true
	d := r.deps
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(d.parentContext(), longOr(d.Timeout))
		defer cancel()
		st, err := d.VMStatus(ctx)
		return boxMsg{state: st.State, err: err}
	}
}

// probeGuest proves what the running box holds.
func (r *root) probeGuest() tea.Cmd {
	r.verifying = true
	d := r.deps
	box := r.facts.Box
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(d.parentContext(), longOr(d.LongTimeout))
		defer cancel()

		f := wizard.Facts{
			Box:             box,
			ServiceDeclared: d.ServiceDeclared,
			Credential:      wizard.CredentialUnknown,
		}

		rep, err := d.Bootstrap(ctx, true)
		f.Bootstrapped = err == nil
		if d.CredentialState != nil {
			f.Credential = d.CredentialState(rep)
		}

		// The rest is only meaningful on a guest that verified. Asking a box
		// that did not would report every answer as a failure to reach it,
		// which reads as a fact about setup rather than about the box.
		if f.Bootstrapped {
			if d.ServiceDeclared && d.ServeStatus != nil {
				if sr, err := d.ServeStatus(ctx); err == nil {
					f.ServeInstalled = sr.Installed
					f.ServeRunning = sr.Ready
				}
			}
			if d.BrainStatus != nil {
				if br, err := d.BrainStatus(ctx); err == nil {
					f.BrainReady = br.State == brain.StateInitialized
				}
			}
		}
		if d.ProjectList != nil {
			if list, err := d.ProjectList(); err == nil {
				f.ProjectCount = len(list)
			}
		}
		return factsMsg{facts: f}
	}
}

func longOr(d time.Duration) time.Duration {
	if d <= 0 {
		return 10 * time.Minute
	}
	return d
}

// run starts one operation, refusing to start a second while one is in flight.
func (r *root) run(label string, long bool, fn func(context.Context) error) tea.Cmd {
	return r.runDetailed(label, long, func(ctx context.Context) (string, error) {
		return "", fn(ctx)
	})
}

// runDetailed is run for an operation whose failure can carry material the
// operator acts on next: a failed add may hold the deploy key to authorize.
func (r *root) runDetailed(label string, long bool, fn func(context.Context) (string, error)) tea.Cmd {
	if r.busy != "" {
		return nil
	}
	r.busy = label
	r.busyStart = time.Now()
	r.errText = ""
	r.errDetail = ""
	r.note = ""
	return func() tea.Msg {
		ctx, cancel := r.opContext(long)
		defer cancel()
		detail, err := fn(ctx)
		return opMsg{label: label, err: err, detail: detail}
	}
}

// canRebind reports whether this build offers the chooser: a seam to run and
// more than one backend to pick from.
func (r *root) canRebind() bool {
	return r.deps.Rebind != nil && len(r.deps.Backends) > 1
}

// openChooser opens the rebind chooser on the current backend. It refuses
// while an operation is in flight, the same way a second operation is
// refused: a rebind tears down every seam the operation is running through.
func (r *root) openChooser() {
	if !r.canRebind() || r.busy != "" {
		return
	}
	r.choosing = true
	r.chooseCursor = 0
	for i, name := range r.deps.Backends {
		if name == r.deps.Backend {
			r.chooseCursor = i
		}
	}
}

func (r *root) chooseKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "esc":
		r.choosing = false
	case "up", "k":
		if r.chooseCursor > 0 {
			r.chooseCursor--
		}
	case "down", "j":
		if r.chooseCursor < len(r.deps.Backends)-1 {
			r.chooseCursor++
		}
	case "enter":
		r.choosing = false
		name := r.deps.Backends[r.chooseCursor]
		// Rebinding to the backend the hub is on would tear the screen down
		// to rebuild the same thing; picking it means closing the chooser.
		if name == r.deps.Backend {
			return nil
		}
		return r.rebind(name)
	}
	return nil
}

// rebind runs the rebind seam off the ui loop, holding the busy lock the way
// any operation does.
func (r *root) rebind(name string) tea.Cmd {
	if r.busy != "" {
		return nil
	}
	r.busy = "rebinding to " + name
	r.busyStart = time.Now()
	r.errText = ""
	r.errDetail = ""
	r.note = ""
	d := r.deps
	return func() tea.Msg {
		nd, err := d.Rebind(name)
		return rebindMsg{name: name, deps: nd, err: err}
	}
}

// handoff resolves an interactive session and gives it the terminal.
//
// Resolving is bounded work like any other operation, because it is the
// preflight the command surface runs: it reaches the guest to prove the
// checkout is one a session can be opened in. The session itself is not bounded
// by that context, which is why only the resolution runs under it.
func (r *root) handoff(label string, build func(context.Context) (execx.InteractiveCommand, error)) tea.Cmd {
	if r.busy != "" || build == nil {
		return nil
	}
	r.busy = label
	r.busyStart = time.Now()
	r.errText = ""
	r.errDetail = ""
	r.note = ""
	return func() tea.Msg {
		ctx, cancel := r.opContext(true)
		defer cancel()
		spec, err := build(ctx)
		return specMsg{label: label, spec: spec, err: err}
	}
}

// sessionTailBytes is how much of a session's standard error the hub keeps.
// It is the size of a refusal and its remedy, not of a transcript: the tail
// exists to name why a session ended, and a session that ran and printed is
// read on the terminal while it runs.
const sessionTailBytes = 2048

// tailBuffer keeps the last sessionTailBytes written to it and discards the
// rest. It is written by the child's copy goroutine and read on the ui loop
// after the child exits, so it holds a lock.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// sessionProcess builds the process one interactive session runs in, and the
// tail that keeps the end of what it said.
//
// The session keeps the operator's real standard error: the tail is a copy
// taken on the way past, so nothing the operator would have seen is withheld
// or delayed. Only standard error is copied. Standard output is where a
// full-screen agent draws, and holding two kilobytes of escape sequences would
// buy nothing.
func sessionProcess(ctx context.Context, spec execx.InteractiveCommand) (*exec.Cmd, *tailBuffer, error) {
	c, err := execx.InteractiveProcess(ctx, spec)
	if err != nil {
		return nil, nil, err
	}
	tail := &tailBuffer{max: sessionTailBytes}
	c.Stderr = io.MultiWriter(os.Stderr, tail)
	return c, tail, nil
}

// execSession releases the terminal to a real session and takes it back when
// the session ends. The child keeps this process group, so an interrupt reaches
// the session rather than the hub, which is the behaviour the equivalent
// command already has.
func execSession(ctx context.Context, spec execx.InteractiveCommand) (tea.Cmd, error) {
	c, tail, err := sessionProcess(ctx, spec)
	if err != nil {
		return nil, err
	}
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return execDoneMsg{err: err, detail: tail.String()}
	}), nil
}

func (r *root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = msg.Width, msg.Height
		return r, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		r.spin, cmd = r.spin.Update(msg)
		return r, cmd

	case tickMsg:
		return r, tick()

	case boxMsg:
		r.probing = false
		if msg.err != nil {
			r.errText = redact.String(msg.err.Error())
			return r, nil
		}
		r.probed = true
		r.facts = wizard.Facts{
			Box:             msg.state,
			ServiceDeclared: r.deps.ServiceDeclared,
			Credential:      wizard.CredentialUnknown,
		}
		if msg.state != lima.StateRunning {
			// Nothing guest-side can be asked of a box that is not running, so
			// what is on screen is already the whole answer.
			r.verified = true
			return r, nil
		}
		r.verified = false
		return r, r.probeGuest()

	case factsMsg:
		r.verifying = false
		r.verified = true
		if msg.err != nil {
			r.errText = redact.String(msg.err.Error())
			return r, nil
		}
		r.facts = msg.facts
		return r, nil

	case opMsg:
		r.busy = ""
		if msg.err != nil {
			r.errText = fmt.Sprintf("%s: %s", msg.label, redact.String(msg.err.Error()))
			r.errDetail = msg.detail
			return r, nil
		}
		r.note = msg.label + " finished"
		return r, r.afterChange()

	case rebindMsg:
		r.busy = ""
		if msg.err != nil {
			r.errText = fmt.Sprintf("rebinding to %s: %s", msg.name, redact.String(msg.err.Error()))
			return r, nil
		}
		nd := msg.deps
		// The binding changes; the program's lifetime does not. The context
		// the hub was started with stays the one signal handling tears down.
		nd.ctx = r.deps.ctx
		r.deps = nd
		// Nothing on screen may survive from the previous box (ADR-0021 P4):
		// every fact is discarded and every screen starts from nothing.
		r.facts = wizard.Facts{}
		r.probed, r.probing, r.verified, r.verifying = false, false, false, false
		r.errText, r.errDetail = "", ""
		r.setup = setupScreen{}
		r.dash = dashScreen{}
		r.projects = projectsScreen{}
		r.brain = brainScreen{}
		r.serve = newServeScreen()
		r.note = "rebound to " + msg.name
		return r, tea.Batch(r.probeFacts(), r.enter())

	case specMsg:
		if msg.err != nil {
			r.busy = ""
			r.errText = fmt.Sprintf("%s: %s", msg.label, redact.String(msg.err.Error()))
			// A materialization that failed closed left the key that makes the
			// failure actionable. Naming the refusal without showing it would
			// tell the operator to authorize something they cannot see.
			var mErr *materializeError
			if errors.As(msg.err, &mErr) && mErr.key != nil {
				r.errDetail = deployKeyDetail(mErr.key)
			}
			return r, nil
		}
		cmd, err := execSession(r.deps.parentContext(), msg.spec)
		if err != nil {
			r.busy = ""
			r.errText = fmt.Sprintf("%s: %s", msg.label, redact.String(err.Error()))
			return r, nil
		}
		return r, cmd

	case execDoneMsg:
		r.busy = ""
		if msg.err != nil {
			// A session ending non-zero is an outcome, not a hub failure, but
			// the operator cannot act on an exit status alone: the hub
			// repainted over whatever the session said on its way out. The
			// banner persists until it is dismissed, and carries the end of
			// that output under it.
			r.errText = "session ended: " + redact.String(msg.err.Error())
			r.errDetail = strings.TrimRight(redact.String(msg.detail), "\n")
		} else {
			r.note = "session ended"
		}
		return r, r.probeFacts()

	case logsMsg, pollMsg, projectsMsg, brainMsg, serveMsg:
		return r, r.delegate(msg)

	case tea.KeyMsg:
		return r.onKey(msg)
	}
	return r, r.delegate(msg)
}

// afterChange re-reads what the action changed. Every action here alters box
// or guest state, so the cheapest correct answer is to re-probe rather than to
// guess which facts moved.
func (r *root) afterChange() tea.Cmd {
	cmds := []tea.Cmd{r.probeFacts()}
	if r.active == screenProjects {
		cmds = append(cmds, r.projects.load(r.deps))
	}
	if r.active == screenServe {
		cmds = append(cmds, r.serve.load(r.deps))
	}
	if r.active == screenBrain {
		cmds = append(cmds, r.brain.load(r.deps))
	}
	return tea.Batch(cmds...)
}

func (r *root) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A screen that is capturing text owns every key except the one that gives
	// the keyboard back. Switching screens out from under a half-typed remote
	// would lose it silently.
	if r.capturing() {
		return r, r.delegate(msg)
	}

	// The chooser is modal: it owns every key until it closes, because the
	// keys underneath it act on a binding the operator is about to leave.
	if r.choosing {
		return r, r.chooseKey(msg)
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return r, tea.Quit
	case "b":
		r.openChooser()
		return r, nil
	case "esc":
		r.errText = ""
		r.errDetail = ""
		return r, r.delegate(msg)
	case "tab":
		r.switchTo(screenOrder[(int(r.active)+1)%len(screenOrder)])
		return r, r.enter()
	case "shift+tab":
		r.switchTo(screenOrder[(int(r.active)-1+len(screenOrder))%len(screenOrder)])
		return r, r.enter()
	case "1", "2", "3", "4", "5":
		idx := int(msg.String()[0] - '1')
		if idx < len(screenOrder) {
			r.switchTo(screenOrder[idx])
			return r, r.enter()
		}
		return r, nil
	case "r":
		return r, r.refresh()
	case "w":
		r.switchTo(screenSetup)
		return r, r.enter()
	}
	return r, r.delegate(msg)
}

// capturing reports whether the active screen owns the keyboard because it is
// reading text. Every screen that opens a field must be listed here: a screen
// that is left out loses a half-typed value to the first character that happens
// to be a global key.
func (r *root) capturing() bool {
	switch r.active {
	case screenSetup:
		return r.setup.capturing()
	case screenProjects:
		return r.projects.capturing()
	default:
		return false
	}
}

func (r *root) switchTo(s screenID) {
	r.active = s
	r.note = ""
}

// enter loads whatever the screen shows on arrival.
func (r *root) enter() tea.Cmd {
	switch r.active {
	case screenDashboard:
		return r.dash.load(r.deps)
	case screenProjects:
		return r.projects.load(r.deps)
	case screenBrain:
		return r.brain.load(r.deps)
	case screenServe:
		return r.serve.load(r.deps)
	default:
		if !r.probed && !r.probing {
			return r.probeFacts()
		}
		return nil
	}
}

func (r *root) refresh() tea.Cmd {
	return tea.Batch(r.probeFacts(), r.enter())
}

// delegate hands a message to the active screen, which returns the work it
// wants done. Screens never run an operation themselves: they ask the root,
// which is the only place that knows whether one is already in flight.
func (r *root) delegate(msg tea.Msg) tea.Cmd {
	switch r.active {
	case screenSetup:
		return r.setup.update(r, msg)
	case screenDashboard:
		return r.dash.update(r, msg)
	case screenProjects:
		return r.projects.update(r, msg)
	case screenBrain:
		return r.brain.update(r, msg)
	case screenServe:
		return r.serve.update(r, msg)
	}
	return nil
}

func (r *root) View() string {
	w := r.width
	if w < 40 {
		w = 80
	}

	var b strings.Builder
	b.WriteString(r.header(w))
	b.WriteString("\n")
	b.WriteString(rule(w))
	b.WriteString("\n")
	b.WriteString(r.tabs())
	b.WriteString("\n\n")
	b.WriteString(r.body(w))
	b.WriteString("\n")
	b.WriteString(rule(w))
	b.WriteString("\n")
	b.WriteString(r.footer())
	return b.String()
}

func (r *root) header(w int) string {
	left := strings.Join([]string{
		styStrong.Render("torio"),
		styDim.Render("·"),
		styMuted.Render("instance") + " " + styText.Render(r.deps.Instance),
		styDim.Render("·"),
		styMuted.Render("backend") + " " + styText.Render(r.deps.Backend),
	}, " ")

	right := boxCell(r.facts.Box, r.probed)
	if r.deps.Version != "" {
		right += "  " + styDim.Render(r.deps.Version)
	}

	gap := w - lipglossWidth(left) - lipglossWidth(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func boxCell(state lima.State, probed bool) string {
	switch {
	case !probed:
		return styDim.Render("· reading box")
	case state == lima.StateRunning:
		return styLive.Render("●") + " " + styText.Render("running")
	case state == lima.StateStopped:
		return styDim.Render("○ stopped")
	case state == lima.StateNotFound:
		return styAmber.Render("○ no box")
	default:
		return styAmber.Render("? " + string(state))
	}
}

func (r *root) tabs() string {
	cells := make([]string, 0, len(screenOrder))
	for i, s := range screenOrder {
		label := fmt.Sprintf("%d %s", i+1, s.title())
		if s == r.active {
			cells = append(cells, styTabOn.Render(label))
			continue
		}
		cells = append(cells, styTabOff.Render(label))
	}
	return strings.Join(cells, " ")
}

func (r *root) body(w int) string {
	var b strings.Builder

	if r.errText != "" {
		b.WriteString(styErrPanel.Width(w - 2).Render(styAmber.Render("failed  ") + styText.Render(r.errText)))
		b.WriteString("\n")
		if r.errDetail != "" {
			b.WriteString("\n" + r.errDetail + "\n")
		}
		b.WriteString("\n")
	}

	switch {
	case r.choosing:
		b.WriteString(r.chooserView())
	case r.active == screenSetup:
		b.WriteString(r.setup.view(r, w))
	case r.active == screenDashboard:
		b.WriteString(r.dash.view(r, w))
	case r.active == screenProjects:
		b.WriteString(r.projects.view(r, w))
	case r.active == screenBrain:
		b.WriteString(r.brain.view(r, w))
	case r.active == screenServe:
		b.WriteString(r.serve.view(r, w))
	}

	switch {
	case r.busy != "":
		b.WriteString("\n\n")
		b.WriteString(r.spin.View())
		b.WriteString(" " + styText.Render(r.busy))
		b.WriteString(styDim.Render(fmt.Sprintf("  %s elapsed", elapsed(time.Since(r.busyStart)))))
	case r.verifying:
		b.WriteString("\n\n" + r.spin.View() + " " + styMuted.Render("verifying the guest"))
	case r.note != "":
		b.WriteString("\n\n" + styMuted.Render(r.note))
	}
	return b.String()
}

func (r *root) footer() string {
	if r.choosing {
		return styDim.Render("↑/↓ pick · enter rebind · esc cancel")
	}
	keys := r.screenKeys()
	// A screen reading text consumes the global keys too, so offering them
	// would name keys that type a character instead of doing what they say.
	if r.capturing() {
		return styDim.Render(keys)
	}
	global := "tab screen · r refresh · q quit"
	if r.canRebind() {
		global = "b backend · " + global
	}
	if keys != "" {
		return styDim.Render(keys + " · " + global)
	}
	return styDim.Render(global)
}

// chooserView is the rebind chooser. Picking a backend rebuilds every seam
// for it and re-probes from nothing; the current one is marked because
// picking it does nothing but close the chooser.
func (r *root) chooserView() string {
	var b strings.Builder
	b.WriteString(styStrong.Render("Rebind the hub") + "\n")
	b.WriteString(styMuted.Render("Every seam is rebuilt for the backend picked, and the screen re-probes from nothing.") + "\n\n")
	for i, name := range r.deps.Backends {
		marker := "  "
		label := styText.Render(name)
		if i == r.chooseCursor {
			marker = styWorking.Render("▸ ")
			label = styStrong.Render(name)
		}
		if name == r.deps.Backend {
			label += styDim.Render("  current")
		}
		b.WriteString(marker + label + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *root) screenKeys() string {
	switch r.active {
	case screenSetup:
		return r.setup.keys(r)
	case screenDashboard:
		return r.dash.keys(r)
	case screenProjects:
		return r.projects.keys(r)
	case screenBrain:
		return r.brain.keys(r)
	case screenServe:
		return r.serve.keys()
	}
	return ""
}

func elapsed(d time.Duration) string {
	d = d.Round(time.Second)
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m == 0 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm %02ds", m, s)
}
