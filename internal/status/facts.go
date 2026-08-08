package status

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

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
	// name is what `ps -o comm=` printed, which the kernel truncates to fifteen
	// characters.
	name string
}

// processArgv is the fixed reading of the guest's process table, restricted to
// the backend's own identity. The user is a compile-time backend declaration,
// never a value read from a previous answer.
func processArgv(user string) []string {
	return []string{"ps", "-o", "pid=,etimes=,comm=", "-u", user}
}

// parseProcesses reads `ps -o pid=,etimes=,comm= -u <user>` output: one line
// per process, the pid, how many seconds it has been running, and the name.
//
// A line it cannot read is skipped rather than failing the whole reading. The
// output is a list of processes and the question asked of it is which of them
// are the agent; one unreadable line cannot make a process that is there
// absent, and failing closed on it would report the box as unknown because of a
// process that has nothing to do with the agent.
func parseProcesses(out []byte) []process {
	var live []process
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
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
		// comm is the last column and the kernel allows a space in it, so it is
		// everything after the two numeric columns rather than a third field.
		name := strings.TrimSpace(strings.Join(fields[2:], " "))
		live = append(live, process{pid: pid, elapsed: time.Duration(secs) * time.Second, name: name})
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

// sessionsNamed selects the agent's own processes out of the guest's table.
//
// The match is on the whole name and not a prefix or a substring, because a
// substring match is how a status surface starts counting an editor, a pager or
// a grep the agent spawned as a second agent. What is being read is the live
// table rather than a record the backend wrote, so there is nothing here that
// can outlive the process it describes — which is the failure a status document
// built on backend-written records has no answer to.
func sessionsNamed(name string, live []process, guestNow time.Time) SessionField {
	out := make([]Session, 0, len(live))
	for _, proc := range live {
		if proc.name != name {
			continue
		}
		out = append(out, Session{
			PID:        proc.pid,
			StartedAt:  guestNow.Add(-proc.elapsed).Format(time.RFC3339),
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
