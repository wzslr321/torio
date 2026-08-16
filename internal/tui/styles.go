package tui

import "github.com/charmbracelet/lipgloss"

// The palette is the one the tmux status line already uses (internal/cli
// statusline). It is repeated as lipgloss colors rather than shared as strings
// because the two renderers speak different escape vocabularies, but the values
// are deliberately identical: an operator watching the bar and the hub at once
// must not have to learn that amber means one thing in one and another in the
// other.
//
// The meanings are fixed across every screen. Live is a thing that is running,
// working is a thing in flight, amber is a thing waiting on a human or a fact
// that could not be proven, and dim is a thing that is off or does not apply.
const (
	colText    = lipgloss.Color("#ccd3e8")
	colMuted   = lipgloss.Color("#8a93ad")
	colDim     = lipgloss.Color("#4a5268")
	colLive    = lipgloss.Color("#5fd48a")
	colWorking = lipgloss.Color("#8fb3ff")
	colAmber   = lipgloss.Color("#f0b24a")
	colDivider = lipgloss.Color("#3a4159")
	colOnAmber = lipgloss.Color("#141927")
)

var (
	styText    = lipgloss.NewStyle().Foreground(colText)
	styMuted   = lipgloss.NewStyle().Foreground(colMuted)
	styDim     = lipgloss.NewStyle().Foreground(colDim)
	styLive    = lipgloss.NewStyle().Foreground(colLive)
	styWorking = lipgloss.NewStyle().Foreground(colWorking)
	styAmber   = lipgloss.NewStyle().Foreground(colAmber)
	styStrong  = lipgloss.NewStyle().Foreground(colText).Bold(true)

	// styAttention is the one inverted treatment in the hub, reserved for a
	// backend that is blocked on the operator. Spending it anywhere else is how
	// the surface stops meaning anything.
	styAttention = lipgloss.NewStyle().Foreground(colOnAmber).Background(colAmber).Bold(true).Padding(0, 1)

	styTabOn  = lipgloss.NewStyle().Foreground(colText).Bold(true).Padding(0, 1)
	styTabOff = lipgloss.NewStyle().Foreground(colMuted).Padding(0, 1)

	styField = lipgloss.NewStyle().Foreground(colText).Background(lipgloss.Color("#1a2033")).Padding(0, 1)
	styBtn   = lipgloss.NewStyle().Foreground(colText).Background(lipgloss.Color("#263354")).Bold(true).Padding(0, 2)

	styErrPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colAmber).
			Padding(0, 1)
)

// rule is the full-width divider that separates the chrome from the screen.
func rule(width int) string {
	if width < 1 {
		return ""
	}
	b := make([]rune, width)
	for i := range b {
		b[i] = '─'
	}
	return styDim.Render(string(b))
}

// checkMark renders a proven/unproven check in the same vocabulary the command
// surface prints, so the two never disagree about what passed.
func checkMark(ok bool) string {
	if ok {
		return styLive.Render("[ok]")
	}
	return styAmber.Render("[!!]")
}
