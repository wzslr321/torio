package cli

import (
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/status"
)

// runningBox is a box with everything proven and nothing happening. Each case
// below changes only the field it is about, so what it is testing is the
// difference rather than the whole literal.
func runningBox(name, backend string) status.Instance {
	return status.Instance{
		Name:     name,
		Box:      "running",
		Backend:  status.BackendField{State: status.Known, Name: backend},
		Session:  status.SessionField{State: status.Known, Sessions: []status.Session{}},
		Waiting:  status.WaitingField{State: status.Known},
		Progress: status.ProgressField{State: status.NotApplicable},
	}
}

// One state is loud and the rest recede. The chip that wants the operator is
// the only one that inverts, and it carries how long it has been waiting,
// because a chip that only changed colour would leave them guessing whether it
// just happened.
func TestTmuxChipPerState(t *testing.T) {
	waiting := runningBox("torio-claude-code", "claude-code")
	waiting.Waiting = status.WaitingField{State: status.Known, Waiting: true, Kind: "permission", AgeSeconds: 420}

	live := runningBox("torio-claude-code", "claude-code")
	live.Session.Sessions = []status.Session{{PID: 1}, {PID: 2}}

	idle := runningBox("torio-claude-code", "claude-code")

	working := runningBox("torio", "hermes")
	working.Session = status.SessionField{State: status.NotApplicable, Sessions: []status.Session{}}
	working.Progress = status.ProgressField{State: status.Known, AgeSeconds: 14}

	quiet := runningBox("torio", "hermes")
	quiet.Session = status.SessionField{State: status.NotApplicable, Sessions: []status.Session{}}

	unreadable := runningBox("torio-claude-code", "claude-code")
	unreadable.Session = status.SessionField{State: status.Unknown, Sessions: []status.Session{}}

	stopped := runningBox("torio", "hermes")
	stopped.Box = "stopped"

	for _, tc := range []struct {
		name string
		in   status.Instance
		want string
	}{
		{"waiting inverts and says how long", waiting,
			"#[fg=" + barWaitingFG + ",bg=" + barWaitingBG + ",bold] claude-code needs you 7m #[default]"},
		{"live sessions get a count", live,
			"#[fg=" + barLive + "]●#[fg=" + barText + "] claude-code 2#[default]"},
		{"a proven empty box is barely there", idle,
			"#[fg=" + barDim + "]○ claude-code#[default]"},
		{"a backend with no sessions shows when it last worked", working,
			"#[fg=" + barWorking + "]·#[fg=" + barMuted + "] hermes 14s#[default]"},
		{"and nothing at all when it never has", quiet,
			"#[fg=" + barDim + "]" + glyphNotApplicable + " hermes#[default]"},
		{"an unanswered question stays amber", unreadable,
			"#[fg=" + barAmber + "]" + glyphUnknown + "#[fg=" + barMuted + "] claude-code#[default]"},
		{"a stopped box says off", stopped,
			"#[fg=" + barDim + "]○ hermes off#[default]"},
	} {
		if got := tmuxCell(tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// The unstyled line carries no escape sequences at all: a prompt counts the
// characters it is given to place the cursor, and a colour Torio chose would be
// counted along with them.
func TestPromptLineCarriesNoEscapes(t *testing.T) {
	waiting := runningBox("torio-claude-code", "claude-code")
	waiting.Waiting = status.WaitingField{State: status.Known, Waiting: true, Kind: "notification", AgeSeconds: 30}
	live := runningBox("torio", "hermes")
	live.Session = status.SessionField{State: status.NotApplicable, Sessions: []status.Session{}}

	line := renderStatusLine(formatPrompt, status.Report{Instances: []status.Instance{live, waiting}})

	want := "hermes " + glyphNotApplicable + promptSeparator + "claude-code NEEDS YOU"
	if line != want {
		t.Errorf("line = %q, want %q", line, want)
	}
	if strings.ContainsAny(line, "\x1b#") {
		t.Errorf("line = %q, want no escape or style sequence", line)
	}
}

// A stopped box cannot be waiting — the poll proves that without entering it —
// but the order still matters, because an ordering that let any other state win
// would eventually hide the one state the surface exists for.
func TestWaitingIsNeverMaskedByAnotherState(t *testing.T) {
	in := runningBox("torio-claude-code", "claude-code")
	in.Box = "stopped"
	in.Waiting = status.WaitingField{State: status.Known, Waiting: true, Kind: "permission", AgeSeconds: 5}

	if got := promptCell(in); got != "NEEDS YOU" {
		t.Errorf("prompt cell = %q, want the waiting state to win", got)
	}
	if got := tmuxCell(in); !strings.Contains(got, "needs you") {
		t.Errorf("tmux chip = %q, want the waiting state to win", got)
	}
}

// A bar has no width to spend on a name that repeats the backend beside it, and
// a box named some other way has nothing else to be told apart by.
func TestShortNameDropsWhatTheBackendAlreadySays(t *testing.T) {
	unknown := runningBox("torio-claude-code", "")
	unknown.Backend = status.BackendField{State: status.Unknown}

	for _, tc := range []struct {
		in   status.Instance
		want string
	}{
		{runningBox("torio", "hermes"), "hermes"},
		{runningBox("torio-claude-code", "claude-code"), "claude-code"},
		{runningBox("daily", "claude-code"), "daily"},
		{unknown, "torio-claude-code"},
	} {
		if got := shortName(tc.in); got != tc.want {
			t.Errorf("shortName(%q) = %q, want %q", tc.in.Name, got, tc.want)
		}
	}
}

// The failure this line format exists to survive. Something refreshes it on a
// timer and shows whatever arrives, so a poll that broke must not render as an
// empty bar — which is indistinguishable from a host where all is well.
func TestABrokenPollSaysSoOnTheLineAndStillFails(t *testing.T) {
	for _, format := range []string{formatTmux, formatPrompt} {
		fake := &fakeLimaRunner{script: []scriptedResp{
			{res: execx.Result{ExitCode: 1, Stderr: []byte("lima: internal error")}},
		}}

		code, stdout, _ := runVMWithFake(t, []string{"status", "--format", format}, fake)

		if code != int(ExitExternal) {
			t.Errorf("%s: exit = %d, want %d", format, code, ExitExternal)
		}
		if !strings.Contains(stdout, unreachableCell) {
			t.Errorf("%s: stdout = %q, want %q on the line", format, stdout, unreachableCell)
		}
	}
}

func TestStatusLineFormatsEndToEnd(t *testing.T) {
	for _, tc := range []struct {
		format string
		want   string
	}{
		{formatPrompt, "hermes off"},
		{formatTmux, "#[fg=" + barDim + "]○ hermes off#[default]"},
	} {
		fake := &fakeLimaRunner{script: []scriptedResp{
			{res: execx.Result{ExitCode: 0, Stdout: []byte(statusListJSON("torio", "Stopped") + "\n")}},
		}}

		code, stdout, stderr := runVMWithFake(t, []string{"status", "--format", tc.format}, fake)

		if code != int(ExitOK) {
			t.Fatalf("%s: exit = %d, want 0; stderr=%q", tc.format, code, stderr)
		}
		if strings.TrimRight(stdout, "\n") != tc.want {
			t.Errorf("%s: stdout = %q, want %q", tc.format, stdout, tc.want)
		}
	}
}

// The envelope is the machine contract and a line is a rendering of it. Asking
// for both would break the one-envelope rule every other command holds to, and
// choosing one silently would leave the operator to find out which.
func TestStatusRefusesTwoOutputsAtOnce(t *testing.T) {
	for _, args := range [][]string{
		{"status", "--json", "--format", "tmux"},
		{"status", "--format", "nonsense"},
	} {
		fake := &fakeLimaRunner{script: []scriptedResp{
			{res: execx.Result{ExitCode: 0, Stdout: []byte(statusListJSON("torio", "Stopped") + "\n")}},
		}}

		code, _, _ := runVMWithFake(t, args, fake)

		if code != int(ExitUsage) {
			t.Errorf("%v: exit = %d, want %d", args, code, ExitUsage)
		}
		if len(fake.calls) != 0 {
			t.Errorf("%v: ran %d host calls, want none before the flags are accepted", args, len(fake.calls))
		}
	}
}
