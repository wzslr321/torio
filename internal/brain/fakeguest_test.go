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
	"sync"

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
	showBrokenCLI   bool
	projectShow     string
	bootstrapErr    error
	lockHeld        bool
	lockStale       bool
	lockToken       string
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

func (f *fakeGuest) Bootstrap(context.Context, lima.BootstrapOptions) (lima.BootstrapReport, error) {
	if f.bootstrapErr != nil {
		return lima.BootstrapReport{}, f.bootstrapErr
	}
	if f.state != lima.StateRunning {
		return lima.BootstrapReport{}, &lima.Error{Op: "bootstrap", Kind: lima.KindNotRunning}
	}
	return lima.BootstrapReport{Instance: lima.InstanceName}, nil
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
	case strings.Contains(joined, "sudo -n -- true"):
		return okResult(""), nil
	case strings.Contains(joined, "mkdir -m 0700 "+lockPath):
		if f.lockHeld {
			return exitResult(1, "", "exists"), nil
		}
		f.lockHeld = true
		return okResult(""), nil
	case strings.Contains(joined, "tee "+lockPath+"/token"):
		f.lockToken = strings.TrimSpace(string(stdin))
		return okResult(""), nil
	case strings.Contains(joined, "stat -c %U:%G %a "+lockPath):
		return okResult("hermes:hermes 700\n"), nil
	case strings.Contains(joined, "find "+lockPath+" -maxdepth 0 -mmin +"+staleLockAge):
		if f.lockStale {
			return okResult(lockPath + "\n"), nil
		}
		return okResult(""), nil
	case strings.Contains(joined, "cat "+lockPath+"/token"):
		return okResult(f.lockToken + "\n"), nil
	case strings.Contains(joined, "mv -T "+lockPath+" "):
		f.lockHeld = false
		f.lockToken = ""
		f.lockStale = false
		return okResult(""), nil
	case strings.Contains(joined, "rm -rf -- "+lockPath+".stale-"):
		return okResult(""), nil
	case strings.Contains(joined, "rm -f -- "+lockPath+"/token"):
		f.lockToken = ""
		return okResult(""), nil
	case strings.Contains(joined, "rmdir "+lockPath):
		f.lockHeld = false
		return okResult(""), nil
	case strings.Contains(joined, "touch "+lockPath):
		return okResult(""), nil
	case strings.Contains(joined, "test -d "+lockPath):
		if f.lockHeld {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
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
	// The `hermes project` fakes below encode a contract hand-verified against a
	// real Hermes v0.19.0 guest, not an assumed one. Two properties matter and
	// must not be "simplified":
	//   1. `show <unknown-slug>` exits 0 with EMPTY stdout and a stderr
	//      diagnostic. Upstream `hermes_cli/main.py` calls `args.func(args)` and
	//      discards the result, so every `return 1` in `projects_cmd.py` is dead
	//      code. A non-zero exit therefore means a broken/missing CLI
	//      (`showBrokenCLI`), never "no such project".
	//   2. `list` output carries slugs and names, never any path.
	case strings.Contains(joined, "hermes project show "+ProjectSlug):
		switch {
		case f.projectShow != "":
			return okResult(f.projectShow), nil
		case f.showBrokenCLI:
			return exitResult(2, "", "usage: hermes project show"), nil
		case f.showMissing:
			return exitResult(0, "", "project: no such project: "+ProjectSlug), nil
		case f.wrongProject:
			return okResult(projectShowOutput("/home/hermes/other")), nil
		case f.registered:
			return okResult(projectShowOutput(Path)), nil
		default:
			return exitResult(0, "", "project: no such project: "+ProjectSlug), nil
		}
	case strings.Contains(joined, "hermes project list"):
		if f.registered {
			return okResult(fmt.Sprintf("* %-24s %s  [1 folder(s)]\n", ProjectSlug, ProjectName)), nil
		}
		return okResult(""), nil
	case strings.Contains(joined, "hermes project create"):
		f.registered = true
		f.showMissing = false
		f.showBrokenCLI = false
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

// projectShowOutput reproduces the block a real `hermes project show <slug>`
// prints for an existing project: a slug/id header, padded fields, and the
// folder list whose first entry repeats the primary path.
func projectShowOutput(primary string) string {
	return ProjectSlug + "  [p_75fa0ebf]\n" +
		"  name:    " + ProjectName + "\n" +
		"  primary: " + primary + "\n" +
		"  folders:\n" +
		"    * " + primary + "\n"
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

type blockingGuest struct {
	base     *fakeGuest
	blockOn  string
	blocked  chan struct{}
	unblock  chan struct{}
	blockMu  sync.Mutex
	didBlock bool
	routeMu  sync.Mutex
}

func (g *blockingGuest) Bootstrap(ctx context.Context, opts lima.BootstrapOptions) (lima.BootstrapReport, error) {
	return g.base.Bootstrap(ctx, opts)
}

func (g *blockingGuest) SSH(ctx context.Context, command []string) (execx.Result, error) {
	g.block(command)
	g.routeMu.Lock()
	defer g.routeMu.Unlock()
	return g.base.SSH(ctx, command)
}

func (g *blockingGuest) SSHInput(ctx context.Context, stdin []byte, command []string) (execx.Result, error) {
	g.block(command)
	g.routeMu.Lock()
	defer g.routeMu.Unlock()
	return g.base.SSHInput(ctx, stdin, command)
}

func (g *blockingGuest) block(command []string) {
	if !strings.Contains(strings.Join(command, " "), g.blockOn) {
		return
	}
	g.blockMu.Lock()
	if g.didBlock {
		g.blockMu.Unlock()
		return
	}
	g.didBlock = true
	g.blockMu.Unlock()
	close(g.blocked)
	<-g.unblock
}

var _ Guest = (*blockingGuest)(nil)
