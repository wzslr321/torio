/*
 * AI-Provenance:
 *   model: Cursor Grok 4.5
 *   harness: Cursor
 *   plugins:
 *     - lean-ai-provenance
 *   skills:
 *     - mark-ai-provenance
 */
package brain

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

type fakeCall struct {
	argv  []string
	stdin []byte
}

type fakeGuest struct {
	state           lima.State
	pathExists      bool
	empty           bool
	owner           string
	group           string
	mode            string
	fstype          string
	scaffold        bool
	gitRepo         bool
	gitDirty        bool
	gitRemote       bool
	registered      bool
	wrongProject    bool
	showMissing     bool
	markdownCount   int
	attachmentCount int
	totalBytes      int64
	failContains    map[string]int
	truncateOn      string
	transportErr    error
	calls           []fakeCall
}

func readyFake() *fakeGuest {
	return &fakeGuest{
		state:        lima.StateRunning,
		pathExists:   true,
		empty:        true,
		owner:        lima.HermesUser,
		group:        lima.HermesUser,
		mode:         "750",
		fstype:       "ext4",
		totalBytes:   4096,
		failContains: map[string]int{},
	}
}

func initializedFake() *fakeGuest {
	f := readyFake()
	f.empty = false
	f.scaffold = true
	f.gitRepo = true
	f.registered = true
	f.markdownCount = 3
	return f
}

func (f *fakeGuest) Status(context.Context) (lima.Status, error) {
	if f.transportErr != nil {
		return lima.Status{}, f.transportErr
	}
	return lima.Status{State: f.state}, nil
}

func (f *fakeGuest) SSH(ctx context.Context, command []string) (execx.Result, error) {
	return f.route(ctx, nil, command)
}

func (f *fakeGuest) SSHInput(ctx context.Context, stdin []byte, command []string) (execx.Result, error) {
	return f.route(ctx, stdin, command)
}

func (f *fakeGuest) route(_ context.Context, stdin []byte, argv []string) (execx.Result, error) {
	f.calls = append(f.calls, fakeCall{argv: append([]string(nil), argv...), stdin: append([]byte(nil), stdin...)})
	if f.transportErr != nil {
		return execx.Result{ExitCode: -1}, f.transportErr
	}
	joined := strings.Join(argv, " ")
	for needle, code := range f.failContains {
		if strings.Contains(joined, needle) {
			return execx.Result{ExitCode: code, Stderr: []byte("synthetic failure")}, nil
		}
	}
	if f.truncateOn != "" && strings.Contains(joined, f.truncateOn) {
		return execx.Result{ExitCode: 0, StdoutTruncated: true}, nil
	}

	switch {
	case strings.Contains(joined, "test -L "+Path):
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "test -d "+Path):
		if f.pathExists {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "stat -c %U:%G %a "+Path):
		return okResult(f.owner + ":" + f.group + " " + f.mode + "\n"), nil
	case strings.Contains(joined, "findmnt -n -o FSTYPE -T "+Path):
		return okResult(f.fstype + "\n"), nil
	case strings.Contains(joined, "find "+Path+" -mindepth 1 -maxdepth 1"):
		if f.empty {
			return okResult(""), nil
		}
		return okResult("."), nil
	case strings.Contains(joined, "find "+Path+" -type l"):
		return okResult(""), nil
	case strings.Contains(joined, "test -f "+Path+"/"):
		if f.scaffold {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "test -d "+Path+"/"):
		if f.scaffold {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "git -C "+Path+" rev-parse --verify HEAD"):
		if f.gitRepo {
			return okResult("0123456789abcdef\n"), nil
		}
		return exitResult(128, "", "not a repository"), nil
	case strings.Contains(joined, "git -C "+Path+" remote"):
		if f.gitRemote {
			return okResult("origin\n"), nil
		}
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+Path+" status --porcelain=v1"):
		if f.gitDirty {
			return okResult("?? private-note-name.md\n"), nil
		}
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+stagingPath+" rev-parse --verify HEAD"):
		return okResult("0123456789abcdef\n"), nil
	case strings.Contains(joined, "git -C "+stagingPath+" remote"):
		return okResult(""), nil
	case strings.Contains(joined, "find "+Path+"/attachments "):
		return okResult(strings.Repeat(".", f.attachmentCount)), nil
	case strings.Contains(joined, "find "+Path+" -type f -name *.md"):
		return okResult(strings.Repeat(".", f.markdownCount)), nil
	case strings.Contains(joined, "du -sb -- "+Path):
		return okResult(fmt.Sprintf("%d\t%s\n", f.totalBytes, Path)), nil
	case strings.Contains(joined, "hermes project show "+ProjectSlug):
		switch {
		case f.showMissing:
			return exitResult(1, "", "not found"), nil
		case f.wrongProject:
			return okResult("name: Other\npath: /home/hermes/other\n"), nil
		case f.registered:
			return okResult("name: Second Brain\npath: " + Path + "\n"), nil
		default:
			return exitResult(1, "", "not found"), nil
		}
	case strings.Contains(joined, "hermes project list"):
		if f.registered {
			return okResult("Second Brain\t" + Path + "\n"), nil
		}
		return okResult(""), nil
	case strings.Contains(joined, "hermes project create"):
		f.registered = true
		return okResult("created\n"), nil
	case strings.Contains(joined, "tee "+stagingPath+"/"):
		return okResult(""), nil
	case strings.Contains(joined, "mv -T "+stagingPath+" "+Path):
		f.pathExists = true
		f.empty = false
		f.scaffold = true
		f.gitRepo = true
		return okResult(""), nil
	case strings.Contains(joined, "install -d"):
		if strings.HasSuffix(joined, " "+Path) {
			f.pathExists = true
			f.empty = true
		}
		return okResult(""), nil
	case strings.Contains(joined, "rmdir "+Path):
		f.pathExists = false
		return okResult(""), nil
	case strings.Contains(joined, "rm -rf -- "+stagingPath),
		strings.Contains(joined, "chmod "),
		strings.Contains(joined, "git -C "+stagingPath+" init"),
		strings.Contains(joined, "git -C "+stagingPath+" add"),
		strings.Contains(joined, "git -C "+stagingPath+" -c user.name="):
		return okResult(""), nil
	}
	return execx.Result{}, fmt.Errorf("unrouted fake guest command: %s", joined)
}

func okResult(stdout string) execx.Result {
	return execx.Result{ExitCode: 0, Stdout: []byte(stdout)}
}

func exitResult(code int, stdout, stderr string) execx.Result {
	return execx.Result{ExitCode: code, Stdout: []byte(stdout), Stderr: []byte(stderr)}
}

func (f *fakeGuest) saw(fragment string) bool {
	for _, call := range f.calls {
		if strings.Contains(strings.Join(call.argv, " "), fragment) {
			return true
		}
	}
	return false
}

func (f *fakeGuest) count(fragment string) int {
	n := 0
	for _, call := range f.calls {
		if strings.Contains(strings.Join(call.argv, " "), fragment) {
			n++
		}
	}
	return n
}

func (f *fakeGuest) payloads() [][]byte {
	var out [][]byte
	for _, call := range f.calls {
		if len(call.stdin) > 0 {
			out = append(out, bytes.Clone(call.stdin))
		}
	}
	return out
}

func (f *fakeGuest) setFailure(fragment string, exitCode int) {
	f.failContains[fragment] = exitCode
}

func (f *fakeGuest) setCounts(markdown, attachments int, bytes int64) {
	f.markdownCount = markdown
	f.attachmentCount = attachments
	f.totalBytes = bytes
}

var _ Guest = (*fakeGuest)(nil)
