package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wzslr321/torio/internal/brain"
	"github.com/wzslr321/torio/internal/redact"
)

type brainMsg struct {
	report brain.StatusReport
	err    error
}

// brainScreen reports the vault and offers the one action that creates it.
// Import is deliberately absent for now: it takes a host path, and a path
// picker is a screen of its own rather than a field on this one.
type brainScreen struct {
	report brain.StatusReport
	loaded bool
	failed string
}

func (s *brainScreen) load(d Deps) tea.Cmd {
	if d.BrainStatus == nil {
		return nil
	}
	timeout := d.Timeout
	statusFn := d.BrainStatus
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), longOr(timeout))
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

	case tea.KeyMsg:
		if msg.String() == "i" && r.deps.BrainInit != nil {
			d := r.deps
			return r.run("creating the Second Brain", true, d.BrainInit)
		}
	}
	return nil
}

func (s *brainScreen) keys() string { return "i create" }

func (s *brainScreen) view(r *root, w int) string {
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
	b.WriteString(line(rep.ProjectRegistered, "registered as a project", ternary(rep.ProjectRegistered, "yes", "no")))
	b.WriteString(line(rep.SkillState != brain.SkillDrift, "retrieval skill", string(rep.SkillState)))
	b.WriteString(line(true, "contents", fmt.Sprintf("%d notes, %d attachments", rep.MarkdownFiles, rep.AttachmentFiles)))

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
