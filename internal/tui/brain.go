package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wzslr321/torio/internal/brain"
	"github.com/wzslr321/torio/internal/redact"
)

type brainMsg struct {
	report brain.StatusReport
	err    error
}

// brainImportPreviewMsg is a finished import preflight: what would move, or
// the refusal that stopped it.
type brainImportPreviewMsg struct {
	report brain.TransferReport
	err    error
}

// brainScreen reports the vault and offers the actions on it: create, sync,
// and import.
type brainScreen struct {
	report brain.StatusReport
	loaded bool
	failed string

	// The import form and the preflight it always runs first. The form takes
	// the host directory and an optional contained subtree; the preflight is
	// the same `--dry-run` the command offers, shown before anything moves.
	importing bool
	fields    []textinput.Model
	focus     int
	preview   *brain.TransferReport
	source    string
	into      string
}

// capturing is true while the import form owns the keyboard, so the root does
// not treat a typed character as a global key.
func (s *brainScreen) capturing() bool { return s.importing }

func (s *brainScreen) load(d Deps) tea.Cmd {
	if d.BrainStatus == nil {
		return nil
	}
	timeout := d.Timeout
	statusFn := d.BrainStatus
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(d.parentContext(), longOr(timeout))
		defer cancel()
		rep, err := statusFn(ctx)
		return brainMsg{report: rep, err: err}
	}
}

func (s *brainScreen) update(r *root, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case brainMsg:
		s.loaded = true
		if msg.err != nil {
			s.failed = redact.String(msg.err.Error())
			return nil
		}
		s.failed = ""
		s.report = msg.report
		return nil

	case brainImportPreviewMsg:
		r.busy = ""
		if msg.err != nil {
			r.errText = "preflighting the import: " + redact.String(msg.err.Error())
			return nil
		}
		rep := msg.report
		s.preview = &rep
		return nil

	case tea.KeyMsg:
		if s.importing {
			return s.updateImportForm(r, msg)
		}
		if s.preview != nil {
			return s.updatePreview(r, msg)
		}
		d := r.deps
		switch msg.String() {
		case "i":
			if d.BrainInit != nil {
				return r.run("creating the Second Brain", true, d.BrainInit)
			}
		case "y":
			if d.BrainSync != nil {
				// Long: it commits, bundles, carries and merges (ADR-0025).
				return r.run("reconciling the Second Brain", true, func(ctx context.Context) error {
					_, err := d.BrainSync(ctx)
					return err
				})
			}
		case "m":
			if d.BrainImport != nil {
				s.openImportForm()
				return textinput.Blink
			}
		}
	}
	return nil
}

func (s *brainScreen) openImportForm() {
	s.fields = make([]textinput.Model, 2)
	for i, ph := range []string{"/path/to/your/notes", "optional-new-subtree"} {
		ti := textinput.New()
		ti.Placeholder = ph
		ti.Prompt = ""
		ti.CharLimit = 400
		ti.Width = 52
		s.fields[i] = ti
	}
	s.focus = 0
	s.fields[0].Focus()
	s.importing = true
}

func (s *brainScreen) closeImportForm() {
	s.importing = false
	s.fields = nil
}

// updateImportForm drives the two fields. Enter runs the preflight, never the
// import: what would move is shown first, because the one command in this
// package that changes Brain data from operator input should not run before
// the operator has seen what it read into its manifest.
func (s *brainScreen) updateImportForm(r *root, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		s.closeImportForm()
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
		source := strings.TrimSpace(s.fields[0].Value())
		into := strings.TrimSpace(s.fields[1].Value())
		if source == "" {
			r.errText = "an import needs the host directory to read"
			return nil
		}
		s.closeImportForm()
		s.source, s.into = source, into
		return s.startPreflight(r)
	}
	var cmd tea.Cmd
	s.fields[s.focus], cmd = s.fields[s.focus].Update(msg)
	return cmd
}

// startPreflight runs the dry-run under the busy lock. Its answer comes back
// as a preview rather than a note, because the answer is a question: import
// this, or walk away.
func (s *brainScreen) startPreflight(r *root) tea.Cmd {
	if r.busy != "" {
		return nil
	}
	r.busy = "preflighting the import"
	r.busyStart = time.Now()
	r.errText = ""
	r.errDetail = ""
	r.note = ""
	d := r.deps
	source, into := s.source, s.into
	return func() tea.Msg {
		ctx, cancel := r.opContext(true)
		defer cancel()
		rep, err := d.BrainImport(ctx, source, into, true)
		return brainImportPreviewMsg{report: rep, err: err}
	}
}

// updatePreview is the confirmation the preflight exists for: enter imports
// what was just shown, esc walks away with nothing moved.
func (s *brainScreen) updatePreview(r *root, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		s.preview = nil
		d := r.deps
		source, into := s.source, s.into
		return r.run("importing the vault", true, func(ctx context.Context) error {
			_, err := d.BrainImport(ctx, source, into, false)
			return err
		})
	case "esc":
		s.preview = nil
	}
	return nil
}

func (s *brainScreen) keys(r *root) string {
	if s.importing {
		return "tab field · enter preflight · esc cancel"
	}
	if s.preview != nil {
		return "enter import · esc cancel"
	}
	parts := []string{"i create"}
	if r.deps.BrainSync != nil {
		parts = append(parts, "y sync")
	}
	if r.deps.BrainImport != nil {
		parts = append(parts, "m import")
	}
	return strings.Join(parts, " · ")
}

func (s *brainScreen) view(r *root, w int) string {
	if s.importing {
		var b strings.Builder
		b.WriteString(styStrong.Render("Import a vault") + "\n")
		b.WriteString(styMuted.Render("Read from a host directory, carried through the verified transport. Nothing is overwritten.") + "\n\n")
		b.WriteString(styText.Render("host directory") + "\n" + s.fields[0].View() + "\n\n")
		b.WriteString(styText.Render("into a new subtree (optional)") + "\n" + s.fields[1].View())
		return b.String()
	}
	if s.preview != nil {
		p := s.preview
		var b strings.Builder
		b.WriteString(styStrong.Render("Import preflight") + "  " + styMuted.Render("nothing has moved yet") + "\n\n")
		fmt.Fprintf(&b, "%s would arrive: %d markdown, %d attachment(s), %d bytes\n",
			styText.Render(fmt.Sprintf("%d file(s)", p.Files)), p.Markdown, p.Attachments, p.Bytes)
		if p.Conflicts > 0 {
			// A conflict is a file the import will not touch; saying the count
			// here is what makes "existing data is never overwritten" checkable
			// before the operator decides.
			b.WriteString(styAmber.Render(fmt.Sprintf("%d conflict(s) would be left untouched", p.Conflicts)) + "\n")
		}
		if skipped := skippedTotal(p.Skipped); skipped > 0 {
			b.WriteString(styMuted.Render(fmt.Sprintf("%d file(s) refused or skipped by the allowlist", skipped)) + "\n")
		}
		b.WriteString("\n" + styBtn.Render("Import") + styDim.Render("  enter import · esc cancel"))
		return b.String()
	}
	switch {
	case s.failed != "":
		return styAmber.Render("the brain could not be read: ") + styText.Render(s.failed)
	case !s.loaded:
		return styMuted.Render("Reading the Second Brain…")
	}

	rep := s.report
	var b strings.Builder
	b.WriteString(styStrong.Render("Second Brain") + "  " + brainStateWord(rep.State) + "\n")
	if rep.Path != "" {
		b.WriteString(styMuted.Render(rep.Path) + "\n")
	}
	b.WriteString("\n")

	b.WriteString(line(rep.PathExists, "vault directory", ternary(rep.PathExists, "present", "absent")))
	b.WriteString(line(rep.NativeFilesystem, "filesystem", orUnknown(rep.FSType)))
	b.WriteString(line(rep.GitState == brain.GitClean, "git worktree", string(rep.GitState)))
	b.WriteString(line(rep.SkillState != brain.SkillDrift, "retrieval skill", string(rep.SkillState)))
	b.WriteString(line(true, "contents", fmt.Sprintf("%d notes, %d attachments", rep.MarkdownFiles, rep.AttachmentFiles)))
	// Where this box stands relative to the one Brain. Being out of step is not
	// a fault, so it is never marked as one: it is what `y` is for (ADR-0025).
	if r.deps.BrainSync != nil {
		replica := "never reconciled"
		if rep.HubRefKnown {
			replica = fmt.Sprintf("%d ahead, %d behind the host vault", rep.AheadOfHub, rep.BehindHub)
		}
		b.WriteString(line(true, "replica", replica))
	}

	if len(rep.Issues) > 0 {
		b.WriteString("\n" + styAmber.Render("issues") + "\n")
		for _, issue := range rep.Issues {
			b.WriteString(styMuted.Render("  "+truncate(issue, w-4)) + "\n")
		}
	}

	if rep.State != brain.StateInitialized {
		b.WriteString("\n" + styBtn.Render("Create brain") + styDim.Render("  press i"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func skippedTotal(skipped map[string]int) int {
	total := 0
	for _, n := range skipped {
		total += n
	}
	return total
}

func line(ok bool, name, detail string) string {
	return fmt.Sprintf("%s %s %s\n", checkMark(ok), styText.Render(pad(name+":", 26)), styMuted.Render(detail))
}

func brainStateWord(st brain.State) string {
	switch st {
	case brain.StateInitialized:
		return styLive.Render("initialized")
	case brain.StateDrift:
		return styAmber.Render("drift")
	default:
		return styDim.Render("not created")
	}
}

func ternary(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "not reported"
	}
	return s
}
