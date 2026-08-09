package status

import (
	"errors"
	"path"
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

// pathFactFormat is printed by GNU find for one exact, fixed filename. Unlike
// stat, find exits zero when that name is absent and non-zero when the directory
// itself could not be read, so absence cannot be confused with a failed probe.
const pathFactFormat = "%p|%u|%m|%T@\n"

// statEntry is one path as the guest reported it.
type statEntry struct {
	path  string
	owner string
	// mode is the GNU numeric permission spelling, which drops leading zeroes.
	mode  string
	mtime time.Time
}

// parsePathFact accepts either no line (the fixed name is absent) or exactly
// one line for the path that was asked for. Anything else is untrusted output,
// not a partial answer.
func parsePathFact(out []byte, wantPath string) (*statEntry, error) {
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil, nil
	}
	if strings.Contains(line, "\n") {
		return nil, errUnreadableRecord
	}
	fields := strings.Split(line, "|")
	if len(fields) != 4 || fields[0] != wantPath {
		return nil, errUnreadableRecord
	}
	seconds, err := strconv.ParseFloat(fields[3], 64)
	if err != nil || seconds < 0 {
		return nil, errUnreadableRecord
	}
	entry := &statEntry{
		path:  fields[0],
		owner: fields[1],
		mode:  fields[2],
		mtime: time.Unix(int64(seconds), 0).UTC(),
	}
	return entry, nil
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
// A missing entry is not a zero. The caller has already established that the
// exact-name lookup completed, and the honest answer when no declared evidence exists is that
// nothing is known about when the backend last progressed.
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

// pathFactArgv asks GNU find about one exact fixed name without enumerating or
// printing any other agent-controlled filename.
func pathFactArgv(p string) []string {
	return []string{"find", path.Dir(p), "-maxdepth", "1", "-name", path.Base(p), "-type", "f", "-printf", pathFactFormat}
}

// The two reasons a poll gives up on a fact that are its own rather than the
// transport's. They are values because a diagnostic reader wants to tell them
// apart from a truncation or a timeout, and neither carries a guest path: what
// could not be read is named by the fact, not by the file.
var (
	// errUnreadableRecord is a guest command that ran and refused to answer.
	errUnreadableRecord = errors.New("guest command could not produce the fact")
	// errUntrustedMarker is a marker file whose ownership or mode puts it
	// outside what the convention allows to be read.
	errUntrustedMarker = errors.New("waiting marker is not owned, not private, or expired")
)
