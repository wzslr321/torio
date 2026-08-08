package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/status"
)

// The shapes `torio status` can print.
//
// The table is the answer to a question the operator asked. The other two are
// an answer to a question nobody asked — they go on a surface that is glanced
// at rather than read — and that difference is what makes them worth rendering
// here rather than leaving to a recipe. The document is still the interface for
// anyone who wants their own; these are the two lines Torio is willing to keep
// working as the schema moves.
const (
	formatTable  = "table"
	formatTmux   = "tmux"
	formatPrompt = "prompt"
)

// statusFormats is the closed set, in the order `--help` should list them.
var statusFormats = []string{formatTable, formatTmux, formatPrompt}

// The bar's palette.
//
// Exactly one state is loud: a box that wants you inverts, so it is found
// without reading. Everything else recedes in proportion to how little it asks
// of the operator, because a bar whose every box arrives in the same colour
// costs the reading it existed to remove.
const (
	barWaitingFG = "#141927"
	barWaitingBG = "#f0b24a"
	barLive      = "#5fd48a"
	barText      = "#ccd3e8"
	barMuted     = "#8a93ad"
	barDim       = "#4a5268"
	barWorking   = "#8fb3ff"
	barAmber     = "#f0b24a"
	barDivider   = "#3a4159"
)

// promptSeparator divides boxes on an unstyled line, where colour is not
// available to do it.
const promptSeparator = "  │  "

// unreachableCell is what a one-line surface says when the poll itself failed.
//
// It is not an empty string, and that is the whole point of having it. A bar
// that renders nothing when Torio breaks looks exactly like a bar reporting a
// quiet host, so the operator learns nothing at the moment there is most to
// learn. The name is prefixed because `torio` is also a box name: this line is
// the tool speaking about itself, not a box called torio going unknown.
const unreachableCell = "torio: " + glyphUnknown

// renderStatusLine renders one report onto a one-line surface.
func renderStatusLine(format string, rep status.Report) string {
	cells := make([]string, 0, len(rep.Instances))
	for _, in := range rep.Instances {
		if format == formatTmux {
			cells = append(cells, tmuxCell(in))
			continue
		}
		cells = append(cells, shortName(in)+" "+promptCell(in))
	}
	if format == formatTmux {
		return strings.Join(cells, fmt.Sprintf("#[fg=%s]  #[default]", barDivider))
	}
	return strings.Join(cells, promptSeparator)
}

// unreachableLine is what to print instead when the poll could not complete.
func unreachableLine(format string) string {
	if format == formatTmux {
		return fmt.Sprintf("#[fg=%s]%s#[default]", barAmber, unreachableCell)
	}
	return unreachableCell
}

// shortName is what a box is called on a one-line surface.
//
// Where the name is one Torio derived, the derived half repeats what the
// backend already says and costs width a bar does not have. A box named any
// other way keeps its own name, because then the name is the only thing
// telling it apart.
func shortName(in status.Instance) string {
	if in.Backend.State != status.Known {
		return in.Name
	}
	if in.Name == config.DefaultInstance || in.Name == config.InstancePrefix+in.Backend.Name {
		return in.Backend.Name
	}
	return in.Name
}

// promptCell is one box on an unstyled line.
//
// It carries no escape sequences at all. A prompt counts the characters it is
// given to decide where the cursor goes, so colour belongs to the shell that
// places this line (`%F{...}` in zsh), not to Torio.
//
// Waiting is tested first, here and in tmuxCell alike: it is the state the
// surface exists for, and no other condition may mask it.
func promptCell(in status.Instance) string {
	switch {
	case in.Waiting.State == status.Known && in.Waiting.Waiting:
		return "NEEDS YOU"
	case in.Box != string(lima.StateRunning):
		return "off"
	case in.Session.State == status.Known:
		return strconv.Itoa(len(in.Session.Sessions))
	case in.Session.State == status.NotApplicable:
		return glyphNotApplicable
	default:
		return glyphUnknown
	}
}

// tmuxCell is one box as a styled chip.
//
// tmux interprets its own `#[...]` sequences in the output of `#()`, so state
// arrives as colour and shape and the words are the confirmation. The words
// stay because colour alone is not an answer: `needs you 7m` says how long, and
// a chip that only turned amber would leave the operator guessing.
//
// Every value interpolated here is an instance name, a backend name or a
// number. That is a property of the document rather than of this function — the
// schema has no free-text field anywhere, deliberately, because this output is
// read by something that interprets escape sequences and the boxes it describes
// run agents that write their own prose.
func tmuxCell(in status.Instance) string {
	n := shortName(in)
	switch {
	case in.Waiting.State == status.Known && in.Waiting.Waiting:
		return fmt.Sprintf("#[fg=%s,bg=%s,bold] %s needs you %s #[default]",
			barWaitingFG, barWaitingBG, n, compactAge(in.Waiting.AgeSeconds))
	case in.Box != string(lima.StateRunning):
		return fmt.Sprintf("#[fg=%s]○ %s off#[default]", barDim, n)
	case in.Session.State == status.Known && len(in.Session.Sessions) > 0:
		return fmt.Sprintf("#[fg=%s]●#[fg=%s] %s %d#[default]",
			barLive, barText, n, len(in.Session.Sessions))
	case in.Session.State == status.Known:
		return fmt.Sprintf("#[fg=%s]○ %s#[default]", barDim, n)
	case in.Session.State == status.NotApplicable && in.Progress.State == status.Known:
		return fmt.Sprintf("#[fg=%s]·#[fg=%s] %s %s#[default]",
			barWorking, barMuted, n, compactAge(in.Progress.AgeSeconds))
	case in.Session.State == status.NotApplicable:
		return fmt.Sprintf("#[fg=%s]%s %s#[default]", barDim, glyphNotApplicable, n)
	default:
		return fmt.Sprintf("#[fg=%s]%s#[fg=%s] %s#[default]", barAmber, glyphUnknown, barMuted, n)
	}
}
