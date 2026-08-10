package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// lipglossWidth is the printable width of a rendered string, which is not its
// byte length: everything the hub renders carries escape sequences, and half
// the header would be pushed off screen by measuring those.
func lipglossWidth(s string) int { return lipgloss.Width(s) }

// pad right-pads to a printable width, leaving already-wider text alone.
func pad(s string, w int) string {
	if n := lipglossWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// joinColumns puts two blocks side by side, the left one fixed at leftWidth.
func joinColumns(left, right string, leftWidth int) string {
	l := strings.Split(left, "\n")
	rr := strings.Split(right, "\n")
	n := max(len(l), len(rr))

	var b strings.Builder
	for i := range n {
		var lc, rc string
		if i < len(l) {
			lc = l[i]
		}
		if i < len(rr) {
			rc = rr[i]
		}
		b.WriteString(pad(lc, leftWidth))
		b.WriteString(rc)
		if i < n-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// wrap breaks prose at a width, on spaces. Everything it is given is Torio's
// own copy, so there is no escape sequence in it to break across a line.
func wrap(s string, width int) string {
	if width < 8 {
		width = 8
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	line := words[0]
	for _, word := range words[1:] {
		if len(line)+1+len(word) > width {
			b.WriteString(line + "\n")
			line = word
			continue
		}
		line += " " + word
	}
	b.WriteString(line)
	return b.String()
}

// truncate shortens to a printable width, marking that it did.
func truncate(s string, width int) string {
	if width <= 1 || lipglossWidth(s) <= width {
		return s
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width-1]) + "…"
}
