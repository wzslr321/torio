package status

import (
	"context"
	"errors"
	"time"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/guestexec"
)

// Box is one instance to report on, as the host enumerated it.
type Box struct {
	// Name is the Lima instance name.
	Name string
	// State is Lima's own word for what the box is doing.
	State string
	// Running reports whether a guest command could reach it at all. It is the
	// gate on every guest command this package makes: a box that is not running
	// is not asked anything, and what it would have been asked is answered from
	// the fact that nothing on it can be executing.
	Running bool
}

// Resolution is what the host learned about a box without entering it: which
// backend it declares, and the implementation of that backend if this binary
// has one.
//
// A zero Resolution is the honest outcome when the box's own document could not
// be read or names something unknown. It is not an error: one unreadable
// document is one box reported as unknown, never a reason to stop answering
// about the others.
type Resolution struct {
	Backend backend.Backend
	Name    string
	// Err is why the declaration could not be resolved. It reaches verbose,
	// redacted diagnostics only; the stable report remains one unknown field.
	Err error
}

// Poller reads one status document.
//
// Its collaborators are function fields rather than concrete types so that the
// poll can be tested against scripted guest output without a VM, and so this
// package does not reach for a transport, a config document or an instance list
// of its own. Every one of them is supplied by the composition root, which is
// the only place that knows how a box is addressed.
type Poller struct {
	// Instances enumerates the boxes to report on.
	Instances func(ctx context.Context) ([]Box, error)
	// Transport returns the guest channel for one instance. It is called only
	// for a running box whose backend declares a probe.
	Transport func(instance string) backend.Transport
	// Resolve reports which backend a box runs.
	Resolve func(instance string) Resolution
	// Diagnose, when set, is told about every fact that degraded to unknown and
	// why. Nothing else records it: an unknown field says only that something
	// could not be proven, and an operator who wants to know which thing needs
	// somewhere for the reason to go.
	Diagnose func(instance, fact string, err error)
}

// Poll reads the status of every enumerated box.
//
// It fails only when the enumeration itself fails. Everything below that
// degrades to unknown on the instance it concerns, because a poll that refused
// to answer about four healthy boxes on account of a fifth would be exactly the
// surface an operator stops looking at.
func (p *Poller) Poll(ctx context.Context) (Report, error) {
	boxes, err := p.Instances(ctx)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Instances: make([]Instance, 0, len(boxes))}
	for _, box := range boxes {
		rep.Instances = append(rep.Instances, p.instance(ctx, box))
	}
	return rep, nil
}

func (p *Poller) instance(ctx context.Context, box Box) Instance {
	inst := Instance{
		Name:     box.Name,
		Box:      box.State,
		Backend:  BackendField{State: Unknown},
		Session:  unknownSession(),
		Waiting:  unknownWaiting(),
		Progress: unknownProgress(),
	}
	stopped := box.State == "stopped"
	if stopped {
		// Host liveness does not depend on resolving the backend document. A
		// stopped box runs no process and waits on nobody even when its config
		// cannot be read; progress remains inside the box and stays unknown.
		inst.Session = SessionField{State: Known, Sessions: []Session{}}
		inst.Waiting = WaitingField{State: Known, Waiting: false, Waits: []Wait{}}
	}

	res := p.Resolve(box.Name)
	if res.Name == "" || res.Backend == nil {
		// Which agent a box runs is the first thing every other question here
		// depends on: without it there is no identity to run a command as and no
		// declaration of what to read. Unknown propagates rather than being
		// filled in from the instance name, which is a derivation and not a
		// declaration.
		err := res.Err
		if err == nil {
			err = errors.New("backend declaration could not be resolved")
		}
		p.diagnose(box.Name, "backend", err)
		return inst
	}
	inst.Backend = BackendField{State: Known, Name: res.Name}
	if stopped {
		return inst
	}

	spec := res.Backend.Status()
	if spec == nil {
		// A backend that declares no probe is answered from the declaration.
		// Asking the guest anyway would be inventing work to justify an answer
		// already given, and reporting a quiet agent would be inventing the
		// answer itself.
		inst.Session = SessionField{State: NotApplicable, Sessions: []Session{}}
		inst.Waiting = WaitingField{State: NotApplicable, Waits: []Wait{}}
		inst.Progress = ProgressField{State: NotApplicable}
		return inst
	}

	if !box.Running {
		// Reaching this branch means Lima reported broken or unknown rather than
		// stopped. No guest command is safe to attempt, but neither state proves
		// the absence of a process, so every agent fact stays unknown.
		return inst
	}

	inst.Session, inst.Waiting, inst.Progress = p.probe(ctx, box.Name, res.Backend.Identity(), spec)
	return inst
}

// probe reads one running box.
//
// Every read is a separate fixed argv over the one-shot transport, in the same
// shape every other guest question in this codebase takes. Combining them into
// one shell invocation would save round trips and cost the property that makes
// the output safe to read: each answer is bounded and refused when truncated on
// its own, and no delimiter an agent-writable file could contain sits between
// two of them.
func (p *Poller) probe(ctx context.Context, instance string, id backend.Identity, spec *backend.StatusSpec) (SessionField, WaitingField, ProgressField) {
	t := p.Transport(instance)
	user := id.GuestUser

	out, err := p.run(ctx, instance, "clock", t, user, "date", "+%s")
	if err != nil {
		return unknownSession(), unknownWaiting(), unknownProgress()
	}
	guestNow, err := parseGuestNow(out)
	if err != nil {
		p.diagnose(instance, "clock", err)
		return unknownSession(), unknownWaiting(), unknownProgress()
	}

	session := p.sessions(ctx, instance, t, user, spec, guestNow)
	progress, marker := p.paths(ctx, instance, t, user, id, spec, guestNow)
	waiting := p.waiting(ctx, instance, t, user, id, spec, session, marker, guestNow)
	return session, waiting, progress
}

// sessions reads the guest's process table and keeps the agent's own processes.
func (p *Poller) sessions(ctx context.Context, instance string, t backend.Transport, user string, spec *backend.StatusSpec, guestNow time.Time) SessionField {
	if spec.SessionProcess == "" {
		// The backend runs no process a session corresponds to. That is a
		// declaration, and reporting it as an agent that is not running would be
		// answering a question this backend was never asked.
		return SessionField{State: NotApplicable, Sessions: []Session{}}
	}
	out, err := p.run(ctx, instance, "processes", t, user, processArgv(user)...)
	if err != nil {
		return unknownSession()
	}
	live, err := parseProcesses(out)
	if err != nil {
		p.diagnose(instance, "processes", err)
		return unknownSession()
	}
	return sessionsNamed(spec.SessionProcess, live, guestNow)
}

// paths reads the progress evidence a backend declared and the marker file's
// own ownership and mode. Each exact name is a separate bounded call so a
// missing file is distinguishable from a failed directory read.
func (p *Poller) paths(ctx context.Context, instance string, t backend.Transport, user string, id backend.Identity, spec *backend.StatusSpec, guestNow time.Time) (ProgressField, *statEntry) {
	paths := append([]string{}, spec.ProgressPaths...)
	markerPath := ""
	if spec.WaitingMarker {
		markerPath = markerPathFor(id)
		paths = append(paths, markerPath)
	}
	if len(paths) == 0 {
		return ProgressField{State: NotApplicable}, nil
	}

	entries := make(map[string]statEntry, len(paths))
	for _, factPath := range paths {
		res, err := guestexec.Run(ctx, t, guestexec.UserExecAs(user, pathFactArgv(factPath)...))
		if err != nil {
			p.diagnose(instance, "paths", err)
			return unknownProgress(), nil
		}
		if res.ExitCode != 0 {
			p.diagnose(instance, "paths", errUnreadableRecord)
			return unknownProgress(), nil
		}
		entry, err := parsePathFact(res.Stdout, factPath)
		if err != nil {
			p.diagnose(instance, "paths", err)
			return unknownProgress(), nil
		}
		if entry != nil {
			entries[factPath] = *entry
		}
	}

	var marker *statEntry
	if markerPath != "" {
		if e, ok := entries[markerPath]; ok {
			marker = &e
		}
	}
	if len(spec.ProgressPaths) == 0 {
		// The path call happened for the marker alone: this backend writes no
		// evidence of work, which is a declaration and not an unreadable box.
		return ProgressField{State: NotApplicable}, marker
	}
	return newestProgress(spec.ProgressPaths, entries, guestNow), marker
}

// waiting decides the one event-carried field.
func (p *Poller) waiting(ctx context.Context, instance string, t backend.Transport, user string, id backend.Identity, spec *backend.StatusSpec, session SessionField, marker *statEntry, guestNow time.Time) WaitingField {
	if !spec.WaitingMarker {
		// The backend has no way to say. That is not "not waiting": an operator
		// told an agent is not waiting stops looking at it.
		return unknownWaiting()
	}
	if session.State != Known {
		// The marker ranks below liveness, so without liveness it cannot be
		// ranked at all.
		return unknownWaiting()
	}
	if marker == nil {
		// Bootstrap initializes an empty document and the hook retains it across
		// clears. Its absence means readiness cannot be proven, not that nobody
		// asked.
		p.diagnose(instance, "waiting", errUnreadableRecord)
		return unknownWaiting()
	}
	if !markerTrusted(*marker, user) {
		p.diagnose(instance, "waiting", errUntrustedMarker)
		return unknownWaiting()
	}
	res, err := guestexec.Run(ctx, t, guestexec.UserExecAs(user, "cat", "--", marker.path))
	if err != nil {
		p.diagnose(instance, "waiting", err)
		return unknownWaiting()
	}
	if res.ExitCode != 0 {
		p.diagnose(instance, "waiting", errUnreadableRecord)
		return unknownWaiting()
	}
	doc, err := decodeMarker(res.Stdout)
	if err != nil {
		p.diagnose(instance, "waiting", err)
		return unknownWaiting()
	}
	return waitingFromMarker(doc, session.Sessions, guestNow)
}

// run executes one fixed argv as the backend identity and returns its standard
// output, treating a non-zero exit as a failure to answer.
func (p *Poller) run(ctx context.Context, instance, fact string, t backend.Transport, user string, argv ...string) ([]byte, error) {
	res, err := guestexec.Run(ctx, t, guestexec.UserExecAs(user, argv...))
	if err != nil {
		p.diagnose(instance, fact, err)
		return nil, err
	}
	if res.ExitCode != 0 {
		p.diagnose(instance, fact, errUnreadableRecord)
		return nil, errUnreadableRecord
	}
	return res.Stdout, nil
}

func (p *Poller) diagnose(instance, fact string, err error) {
	if p.Diagnose != nil {
		p.Diagnose(instance, fact, err)
	}
}

// markerPathFor is where a backend's hooks write the waiting marker: in the
// identity's own home, which is the one directory on the guest that identity
// owns and no other agent can write.
func markerPathFor(id backend.Identity) string { return id.Home + "/" + MarkerFileName }
