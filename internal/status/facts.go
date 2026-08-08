package status

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/wzslr321/torio/internal/backend"
)

// startTolerance is how far a backend's recorded start time may sit from the
// one derived from the live process before the two are treated as different
// processes.
//
// It exists because the two clocks are read differently: a backend writes a
// wall-clock timestamp when it starts a session, while the poll derives one by
// subtracting the process's elapsed seconds from the guest's current time,
// which is whole-second and rounds. A minute and a half absorbs that without
// absorbing a pid recycled into a genuinely different process — pids are handed
// out in sequence and a box would have to churn through the whole pid space
// inside the window for a collision to land here.
const startTolerance = 90 * time.Second

// parseGuestNow reads `date +%s` from the guest.
//
// Every age this package reports is computed against it rather than against the
// host's clock. A VM's clock drifts across a laptop suspend, and an age
// computed across that drift reports an agent as idle for the length of the
// operator's lunch break, or as having progressed in the future.
func parseGuestNow(out []byte) (time.Time, error) {
	s := strings.TrimSpace(string(out))
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, errors.New("guest clock is not a unix timestamp")
	}
	return time.Unix(secs, 0).UTC(), nil
}

// process is one live process on the guest.
type process struct {
	pid     int
	elapsed time.Duration
}

// parseProcesses reads `ps -o pid=,etimes= -u <user>` output: one line per
// process, the pid and how many seconds it has been running.
//
// A line it cannot read is skipped rather than failing the whole reading. The
// output is a list of processes and the question asked of it is whether a
// specific pid is among them; one unreadable line cannot make a pid that is
// there absent, and failing closed on it would report every session on the box
// as unknown because of a process that has nothing to do with any of them.
func parseProcesses(out []byte) map[int]process {
	live := make(map[int]process)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		secs, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || secs < 0 {
			continue
		}
		live[pid] = process{pid: pid, elapsed: time.Duration(secs) * time.Second}
	}
	return live
}

// statFormat is the `stat -c` format every path fact is read through. The
// separator is a pipe rather than whitespace because a mode and an owner never
// contain one, and every path this package stats is a compile-time constant.
const statFormat = "%n|%U|%a|%Y"

// statEntry is one path as the guest reported it.
type statEntry struct {
	path  string
	owner string
	// mode is the `stat -c %a` spelling, which drops leading zeroes.
	mode  string
	mtime time.Time
}

// parseStatEntries reads the output of one `stat` over several paths.
//
// A path that does not exist makes `stat` write to standard error and exit
// non-zero while still printing a line for every path that does, so the caller
// passes stdout here regardless of the exit code and absence shows up as a
// missing entry. That is the intended reading: the files this stats are
// evidence a backend leaves behind, and a backend that has not run yet leaves
// none.
func parseStatEntries(out []byte) map[string]statEntry {
	entries := make(map[string]statEntry)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) != 4 {
			continue
		}
		secs, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			continue
		}
		entries[fields[0]] = statEntry{
			path:  fields[0],
			owner: fields[1],
			mode:  fields[2],
			mtime: time.Unix(secs, 0).UTC(),
		}
	}
	return entries
}

// writableBeyondOwner reports whether a `stat -c %a` mode grants write to the
// group or to everyone. An unparseable mode counts as writable, so a mode the
// poll cannot read fails closed.
func writableBeyondOwner(mode string) bool {
	bits, err := strconv.ParseInt(mode, 8, 32)
	if err != nil {
		return true
	}
	return bits&0o022 != 0
}

// verifySessions keeps the sessions a backend claims only where a live process
// agrees with the claim.
//
// The claim alone is not evidence: a backend that is killed leaves its record
// behind, so a record read on its own reports a dead agent as working forever.
// The start time is what stops the opposite error — a pid the guest has since
// handed to something else would otherwise let an unrelated process stand in
// for the agent that died.
func verifySessions(claims []backend.SessionFact, live map[int]process, guestNow time.Time) SessionField {
	out := make([]Session, 0, len(claims))
	for _, claim := range claims {
		proc, running := live[claim.PID]
		if !running {
			continue
		}
		startedAt := guestNow.Add(-proc.elapsed)
		if !claim.StartedAt.IsZero() {
			drift := startedAt.Sub(claim.StartedAt.UTC())
			if drift < 0 {
				drift = -drift
			}
			if drift > startTolerance {
				continue
			}
		}
		out = append(out, Session{
			PID:        claim.PID,
			StartedAt:  startedAt.Format(time.RFC3339),
			AgeSeconds: int64(proc.elapsed / time.Second),
		})
	}
	return SessionField{State: Known, Sessions: out}
}

// newestProgress is the most recent modification time among the paths a backend
// declared as evidence of work.
//
// A path that is absent is not an error and not a zero: a backend that has
// never run has written none of them, and the honest answer is that nothing is
// known about when it last progressed.
func newestProgress(paths []string, entries map[string]statEntry, guestNow time.Time) ProgressField {
	var newest time.Time
	for _, p := range paths {
		e, ok := entries[p]
		if !ok {
			continue
		}
		if e.mtime.After(newest) {
			newest = e.mtime
		}
	}
	if newest.IsZero() {
		return unknownProgress()
	}
	age := int64(guestNow.Sub(newest) / time.Second)
	if age < 0 {
		// A future mtime is a clock the poll cannot reason about. Reporting a
		// negative age would render as progress that has not happened yet.
		return unknownProgress()
	}
	return ProgressField{State: Known, At: newest.Format(time.RFC3339), AgeSeconds: age}
}

// statArgv builds the single `stat` call that covers every path a poll needs
// from one instance.
func statArgv(paths []string) []string {
	argv := make([]string, 0, len(paths)+2)
	argv = append(argv, "stat", "-c", statFormat)
	return append(argv, paths...)
}

// The two reasons a poll gives up on a fact that are its own rather than the
// transport's. They are values because a diagnostic reader wants to tell them
// apart from a truncation or a timeout, and neither carries a guest path: what
// could not be read is named by the fact, not by the file.
var (
	// errUnreadableRecord is a guest command that ran and refused to answer.
	errUnreadableRecord = errors.New("guest command could not produce the fact")
	// errUntrustedMarker is a marker file whose ownership, mode or age puts it
	// outside what the convention allows to be read.
	errUntrustedMarker = errors.New("waiting marker is not owned, not private, or expired")
)
