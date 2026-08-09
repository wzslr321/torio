package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/status"
)

// The two glyphs that stand in for an answer nobody gave.
//
// They are deliberately not a zero and not a blank. On a host running several
// backends most of any row is "not knowable here", and an operator who reads
// that as "all quiet" learns to ignore the whole surface — which is the failure
// a status line exists to prevent.
const (
	// glyphNotApplicable marks a question this backend was never asked.
	glyphNotApplicable = "—"
	// glyphUnknown marks a question that was asked and could not be answered.
	glyphUnknown = "?"
)

func newStatusCmd(a *app) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report every Torio box and what its agent is doing",
		Long: "Poll every box Torio owns and report, for each, whether it is running, which " +
			"backend it was provisioned for, what that backend has running, whether anything " +
			"there is waiting on a human, and when it last provably did work.\n\n" +
			"It answers across boxes; the per-box commands answer in depth. For one box's " +
			"bootstrap checks see `torio backend status`, and for its guest service see " +
			"`torio serve status`.\n\n" +
			"Every field is one of three things: a proven value, `?` for a question that could " +
			"not be answered right now, or `—` for one this backend does not answer at all. " +
			"A field is never guessed, so the command exits 0 whenever the poll completes; " +
			"only a failure to list the boxes at all is an error.\n\n" +
			"The poll covers the default box, every box whose name Torio derived from a " +
			"backend, and the box TORIO_INSTANCE names for this invocation. A box named " +
			"directly by any other means is outside it. `--config` does not redirect the " +
			"documents read here: each box's backend is read from the document that box owns.\n\n" +
			"`--format tmux` and `--format prompt` collapse the report onto one line for a " +
			"status bar or a shell prompt. `torio status setup` prints the configuration that " +
			"puts one there.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.checkStatusFormat(format); err != nil {
				return err
			}
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			rep, err := a.newPoller().Poll(ctx)
			if err != nil {
				// A one-line surface is refreshed on a timer by something that
				// shows whatever arrives, so silence there reads as a quiet
				// host rather than as a broken poll. Say so on the line, and
				// still fail: the exit code is what a script reads.
				if format != formatTable {
					fmt.Fprintln(a.stdout, unreachableLine(format))
				}
				return mapLimaError("status", err)
			}
			return a.emitStatus(rep, format)
		},
	}
	cmd.Flags().StringVar(&format, "format", formatTable,
		"how to print the report: "+strings.Join(statusFormats, ", "))
	cmd.AddCommand(newStatusSetupCmd(a))
	return cmd
}

// checkStatusFormat refuses a format this build does not have, and refuses the
// one combination that asks for two answers at once.
//
// `--json` is the machine contract — one envelope, schema-versioned — and a
// line is a rendering of it. Printing both would break the single-envelope rule
// every other command holds to; picking one silently would make the operator
// guess which. Neither is worth the convenience.
func (a *app) checkStatusFormat(format string) error {
	if !slices.Contains(statusFormats, format) {
		return usageError(fmt.Sprintf("unknown --format %q; known formats are %s",
			format, strings.Join(statusFormats, ", ")))
	}
	if a.jsonOut && format != formatTable {
		return usageError(fmt.Sprintf(
			"--json and --format %s are two different outputs; pass one", format))
	}
	return nil
}

// newPoller wires the status poll to this invocation's host access.
//
// One adapter is built and scoped per box. The instance this invocation
// resolved is passed to the enumeration as well, so a box TORIO_INSTANCE named
// directly — which bears no name Torio could recognize — is still reported
// rather than silently missing from the one surface that claims to cover
// everything.
func (a *app) newPoller() *status.Poller {
	adapter := a.newLima()
	return &status.Poller{
		Instances: func(ctx context.Context) ([]status.Box, error) {
			infos, err := adapter.ListTorioInstances(ctx, a.instance)
			if err != nil {
				return nil, err
			}
			boxes := make([]status.Box, 0, len(infos))
			for _, in := range infos {
				boxes = append(boxes, status.Box{
					Name:    in.Name,
					State:   string(in.State),
					Running: in.State == lima.StateRunning,
				})
			}
			return boxes, nil
		},
		Transport: func(instance string) backend.Transport { return adapter.ForInstance(instance) },
		Resolve:   a.resolveBoxBackend,
		Diagnose: func(instance, fact string, err error) {
			a.logger.Debug("status fact unproven", "instance", instance, "fact", fact, "reason", err)
		},
	}
}

// resolveBoxBackend reads which backend one box runs.
//
// The box's own document is the authority, and the derived name is the fallback
// for a box that has one but declares nothing — a document written before
// instances recorded a backend names none, and such a box runs the default one.
// A document that cannot be read at all leaves the box unknown: one unreadable
// document is one unknown row, never a reason to stop reporting the others.
func (a *app) resolveBoxBackend(instance string) status.Resolution {
	rt, err := config.LoadInstance(instance, a.configOptions())
	if err != nil {
		return status.Resolution{Err: err}
	}
	name := rt.File.Backend
	if name == "" {
		name = backendForDerivedInstance(instance)
		if name == "" {
			// ADR-0009 defines an absent declaration as Hermes for every
			// instance, including one TORIO_INSTANCE named directly. The
			// derived name is only a stronger hint where one exists.
			name = backend.DefaultName
		}
	}
	b, err := backend.Lookup(name)
	if err != nil {
		return status.Resolution{Err: err}
	}
	return status.Resolution{Backend: b, Name: name}
}

// backendForDerivedInstance reverses the name derivation. It answers only for
// names Torio itself would have produced; anything else is empty and the
// caller applies ADR-0009's default backend.
func backendForDerivedInstance(instance string) string {
	if instance == config.DefaultInstance {
		return backend.DefaultName
	}
	if name, ok := strings.CutPrefix(instance, config.InstancePrefix); ok {
		return name
	}
	return ""
}

func (a *app) emitStatus(rep status.Report, format string) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("status", rep))
	}
	if format != formatTable {
		// An empty line on a host with no boxes is the correct answer there,
		// and the only one that does not take space on a surface the operator
		// did not ask for.
		_, err := fmt.Fprintln(a.stdout, renderStatusLine(format, rep))
		return err
	}
	if len(rep.Instances) == 0 {
		_, err := fmt.Fprint(a.stdout, "no instances\nnext: torio vm init\n")
		return err
	}
	if _, err := fmt.Fprint(a.stdout, "INSTANCE\tBOX\tBACKEND\tSESSION\tWAITING\tPROGRESS\n"); err != nil {
		return err
	}
	for _, in := range rep.Instances {
		if _, err := fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\n",
			in.Name, in.Box, backendCell(in.Backend),
			sessionCell(in.Session), waitingCell(in.Waiting), progressCell(in.Progress)); err != nil {
			return err
		}
	}
	return nil
}

func backendCell(f status.BackendField) string {
	if f.State != status.Known {
		return glyphUnknown
	}
	return f.Name
}

func sessionCell(f status.SessionField) string {
	switch f.State {
	case status.NotApplicable:
		return glyphNotApplicable
	case status.Known:
		return strconv.Itoa(len(f.Sessions))
	default:
		return glyphUnknown
	}
}

// waitingCell names the first session and counts the rest — on a box running
// two agents, "something here wants you" is only half an answer. Every value is
// numeric or a bounded identifier; nothing an agent wrote reaches this line.
func waitingCell(f status.WaitingField) string {
	switch {
	case f.State == status.NotApplicable:
		return glyphNotApplicable
	case f.State != status.Known:
		return glyphUnknown
	case !f.Waiting:
		return "no"
	case len(f.Waits) > 0:
		first := f.Waits[0]
		cell := "yes " + compactAge(first.AgeSeconds) + " pid " + strconv.Itoa(first.PID)
		if len(f.Waits) > 1 {
			cell += " +" + strconv.Itoa(len(f.Waits)-1)
		}
		return cell
	default:
		return glyphUnknown
	}
}

func progressCell(f status.ProgressField) string {
	switch f.State {
	case status.NotApplicable:
		return glyphNotApplicable
	case status.Known:
		return compactAge(f.AgeSeconds)
	default:
		return glyphUnknown
	}
}

// compactAge renders a duration in the largest unit that keeps it readable at a
// glance, which is what a status line is read at.
func compactAge(seconds int64) string {
	d := time.Duration(seconds) * time.Second
	switch {
	case d < time.Minute:
		return strconv.FormatInt(seconds, 10) + "s"
	case d < time.Hour:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	case d < 24*time.Hour:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	default:
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	}
}
