package projects

import (
	"context"
	"fmt"
	"strings"

	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/serve"
)

// The fixtures below describe one project. They are the values every test
// asserts against, so a drifting derived path or remote fails loudly.
const (
	testID     = "demo"
	testName   = "Demo"
	testRemote = "git@github.com:owner/demo.git"
	testPath   = lima.HermesWorkspacePath + "/" + testID
	testOwner  = "operator"
	// testSecret is not a credential. It is a marker the guest doubles put into
	// the output a real Git failure would carry, so a test can prove no error,
	// report or note ever repeats it.
	testSecret = "not-a-real-token-0000"
	// testAgentSocket is a plausible SSH_AUTH_SOCK value. It is a path, not a
	// secret: the socket only grants anything to a process that can open it.
	testAgentSocket = "/private/tmp/com.apple.launchd.0000/Listeners"
)

// fakeAgent is the host SSH agent double. It reports a socket and an identity
// count, which is all this package is allowed to learn about an agent.
type fakeAgent struct {
	socket     string
	identities int
	err        error
	calls      int
}

func (f *fakeAgent) Socket() string { return f.socket }

func (f *fakeAgent) Identities(context.Context) (int, error) {
	f.calls++
	return f.identities, f.err
}

var _ SSHAgent = (*fakeAgent)(nil)

// remoteFailureStderr is the shape Git actually fails with when a remote cannot
// be read: a diagnostic that quotes the URL, credential and all.
const remoteFailureStderr = "fatal: Authentication failed for 'https://user:" + testSecret + "@github.invalid/owner/demo.git/'"

type fakeCall struct {
	argv []string
}

// fakeGuest is the typed guest double. Its `hermes project` routes reproduce a
// contract read from the real Hermes v0.19.0 source on the guest
// (`hermes_cli/projects_cmd.py`, `hermes_cli/projects_db.py`), not an assumed
// one:
//
//   - every subcommand exits 0 even when it fails, because
//     `hermes_cli/main.py` calls `args.func(args)` and discards the return
//     value, so each `return 1` is dead code;
//   - an unknown slug therefore means exit 0, EMPTY stdout and the stderr line
//     `project: no such project: <slug>`;
//   - `show` prints `<slug>  [<id>]` plus ` (archived)` when archived, and
//     resolves archived projects too (`get_project` has no archived filter);
//   - `create` silently allocates `<slug>-2` when the slug is taken
//     (`_unique_slug`), so a blind create can never be trusted;
//   - `list` output carries slugs and names, never a path, and hides archived
//     projects unless `--all` is passed.
//
// A non-zero exit consequently means the Hermes CLI itself is broken or absent,
// never "no such project" — modelled by hermesShowExit / hermesListExit.
type fakeGuest struct {
	bootstrapErr error
	transportErr error

	// Guest checkout at testPath.
	pathExists  bool
	pathSymlink bool
	pathIsFile  bool
	isRepo      bool
	topLevel    string
	origin      string
	shallow     bool
	dirty       bool
	credHelper  bool
	owner       string
	group       string
	mode        string

	// remote is the URL the guest holds and serves; remoteReadable decides
	// whether the noninteractive preflight can read it.
	remote         string
	remoteReadable bool

	// Hermes project registry.
	hermesPresent bool
	// hermesCreateNoop models the real failure shape of `hermes project create`:
	// exit 0, a stderr diagnostic, and no project created.
	hermesCreateNoop bool
	hermesArchived   bool
	hermesPrimary    string
	hermesActive     string
	hermesShowExit   int
	hermesListExit   int
	// hermesShowOutput overrides the `show` block verbatim, so a test can feed
	// output the parser must refuse.
	hermesShowOutput string
	// useSilent models `hermes project use` exiting 0 without confirming.
	useSilent bool

	// hermesUID is what `id -u hermes` prints, and serviceEnvironment is the
	// `Environment=` property of the backend user unit. Together they are the
	// post-session read-only look at the persistent service environment.
	hermesUID          string
	serviceEnvironment string

	// Per-user global safe.directory entries.
	safeDirs map[string][]string

	failContains map[string]int
	truncateOn   string
	calls        []fakeCall
}

// readyFake is a bootstrap-verified guest with a reachable remote, no checkout
// and an empty Hermes project registry: the shape `Add` clones into.
func readyFake() *fakeGuest {
	return &fakeGuest{
		remote:             testRemote,
		remoteReadable:     true,
		owner:              lima.HermesUser,
		group:              sharedGroup,
		mode:               "2775",
		safeDirs:           map[string][]string{},
		failContains:       map[string]int{},
		hermesUID:          "1001",
		serviceEnvironment: "HERMES_HOME=" + lima.HermesProfilePath,
	}
}

// attachedFake is a guest that already holds a compliant checkout and a Hermes
// project pointing at it: the shape a rerun of `Add` must accept unchanged.
func attachedFake() *fakeGuest {
	f := readyFake()
	f.pathExists = true
	f.isRepo = true
	f.origin = f.remote
	f.hermesPresent = true
	f.hermesPrimary = testPath
	f.safeDirs[lima.HermesUser] = []string{testPath}
	f.safeDirs[testOwner] = []string{testPath}
	return f
}

func (f *fakeGuest) Bootstrap(context.Context, lima.BootstrapOptions) (lima.BootstrapReport, error) {
	if f.bootstrapErr != nil {
		return lima.BootstrapReport{}, f.bootstrapErr
	}
	return lima.BootstrapReport{Instance: lima.InstanceName}, nil
}

func (f *fakeGuest) SSH(_ context.Context, argv []string) (execx.Result, error) {
	f.calls = append(f.calls, fakeCall{argv: append([]string(nil), argv...)})
	if f.transportErr != nil {
		return execx.Result{ExitCode: -1}, f.transportErr
	}
	joined := strings.Join(argv, " ")
	for needle, code := range f.failContains {
		if strings.Contains(joined, needle) {
			return exitResult(code, "", "synthetic failure"), nil
		}
	}
	if f.truncateOn != "" && strings.Contains(joined, f.truncateOn) {
		return execx.Result{ExitCode: 0, StdoutTruncated: true}, nil
	}
	return f.route(joined)
}

func (f *fakeGuest) route(joined string) (execx.Result, error) {
	switch {
	case strings.Contains(joined, "sudo -n -- true"):
		return okResult(""), nil

	// --- path shape ---
	case strings.Contains(joined, "test -L "+testPath):
		return boolResult(f.pathSymlink), nil
	case strings.Contains(joined, "test -e "+testPath):
		return boolResult(f.pathExists || f.pathSymlink || f.pathIsFile), nil
	case strings.Contains(joined, "test -d "+testPath):
		return boolResult(f.pathExists && !f.pathIsFile), nil
	case strings.Contains(joined, "stat -c %U:%G %a "+testPath):
		if !f.pathExists {
			return exitResult(1, "", "stat: cannot statx"), nil
		}
		return okResult(f.owner + ":" + f.group + " " + f.mode + "\n"), nil

	// --- remote preflight and clone ---
	case strings.Contains(joined, "git ls-remote -- "+f.remote):
		if !f.remoteReadable {
			return exitResult(128, "", remoteFailureStderr), nil
		}
		return okResult("0123456789abcdef\tHEAD\n"), nil
	case strings.Contains(joined, "git clone -- "+f.remote+" "+testPath):
		if !f.remoteReadable {
			return exitResult(128, "", remoteFailureStderr), nil
		}
		f.pathExists = true
		f.isRepo = true
		f.origin = f.remote
		f.owner = lima.HermesUser
		f.group = lima.HermesUser
		f.mode = "755"
		return okResult(""), nil

	// --- checkout inspection ---
	case strings.Contains(joined, "git -C "+testPath+" rev-parse --show-toplevel"):
		if !f.isRepo {
			return exitResult(128, "", "fatal: not a git repository"), nil
		}
		if f.topLevel != "" {
			return okResult(f.topLevel + "\n"), nil
		}
		return okResult(testPath + "\n"), nil
	case strings.Contains(joined, "git -C "+testPath+" config --get remote.origin.url"):
		if !f.isRepo || f.origin == "" {
			return exitResult(1, "", ""), nil
		}
		return okResult(f.origin + "\n"), nil
	case strings.Contains(joined, "git -C "+testPath+" rev-parse --is-shallow-repository"):
		if f.shallow {
			return okResult("true\n"), nil
		}
		return okResult("false\n"), nil
	case strings.Contains(joined, "git -C "+testPath+" status --porcelain=v1"):
		if f.dirty {
			return okResult(" M src/main.go\n"), nil
		}
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+testPath+" config --local --get-regexp"):
		if f.credHelper {
			return okResult("credential.helper store\n"), nil
		}
		return exitResult(1, "", ""), nil

	// --- shared permissions ---
	case strings.Contains(joined, "chown -R "+lima.HermesUser+":"+sharedGroup+" -- "+testPath):
		f.owner = lima.HermesUser
		f.group = sharedGroup
		return okResult(""), nil
	case strings.Contains(joined, "chmod -R g+rwX -- "+testPath):
		return okResult(""), nil
	case strings.Contains(joined, "find "+testPath+" -type d -exec chmod g+s"):
		f.mode = "2775"
		return okResult(""), nil

	// --- safe.directory for the two trusted identities ---
	case strings.Contains(joined, "git config --global --get-all safe.directory"):
		entries := f.safeDirs[userOf(joined)]
		if len(entries) == 0 {
			return exitResult(1, "", ""), nil
		}
		return okResult(strings.Join(entries, "\n") + "\n"), nil
	case strings.Contains(joined, "git config --global --add safe.directory "):
		user := userOf(joined)
		f.safeDirs[user] = append(f.safeDirs[user], testPath)
		return okResult(""), nil

	// --- persistent backend environment (read-only) ---
	case strings.Contains(joined, "id -u "+lima.HermesUser):
		return okResult(f.hermesUID + "\n"), nil
	case strings.Contains(joined, "systemctl --user show "+serve.UnitName):
		return okResult("Environment=" + f.serviceEnvironment + "\n"), nil

	// --- hermes project ---
	case strings.Contains(joined, "hermes project show "):
		if f.hermesShowExit != 0 {
			return exitResult(f.hermesShowExit, "", "usage: hermes project show"), nil
		}
		if f.hermesShowOutput != "" {
			return okResult(f.hermesShowOutput), nil
		}
		if !f.hermesPresent {
			return exitResult(0, "", "project: no such project: "+testID), nil
		}
		return okResult(projectShowOutput(testID, testName, f.hermesPrimary, f.hermesArchived)), nil
	case strings.Contains(joined, "hermes project list"):
		if f.hermesListExit != 0 {
			return exitResult(f.hermesListExit, "", "usage: hermes project list"), nil
		}
		if !f.hermesPresent || f.hermesArchived {
			return okResult("No projects yet. Create one with `hermes project create <name>`.\n"), nil
		}
		return okResult(fmt.Sprintf("  %-24s %s  [1 folder(s)]\n", testID, testName)), nil
	case strings.Contains(joined, "hermes project create "):
		// Real `create` never fails by exit code and silently suffixes a taken
		// slug, so the double registers under our slug only when it is free.
		if f.hermesCreateNoop {
			return exitResult(0, "", "project: could not create project"), nil
		}
		if !f.hermesPresent {
			f.hermesPresent = true
			f.hermesArchived = false
			f.hermesPrimary = testPath
		}
		return okResult("Created project " + testID + " (p_0123abcd)\n"), nil
	case strings.Contains(joined, "hermes project archive "):
		if !f.hermesPresent {
			return exitResult(0, "", "project: no such project: "+testID), nil
		}
		f.hermesArchived = true
		return okResult("Archived " + testID + "\n"), nil
	case strings.Contains(joined, "hermes project restore "):
		if !f.hermesPresent {
			return exitResult(0, "", "project: no such project: "+testID), nil
		}
		f.hermesArchived = false
		return okResult("Restored " + testID + "\n"), nil
	case strings.Contains(joined, "hermes project use "):
		if !f.hermesPresent || f.useSilent {
			return exitResult(0, "", "project: no such project: "+testID), nil
		}
		f.hermesActive = testID
		return okResult("Active project: " + testID + "\n"), nil
	}
	return execx.Result{}, fmt.Errorf("unrouted fake guest command: %s", joined)
}

// userOf extracts the identity a `sudo -n -u <user> --` argv runs as.
func userOf(joined string) string {
	fields := strings.Fields(joined)
	for i, f := range fields {
		if f == "-u" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

// projectShowOutput reproduces the block `_print_project` writes for an
// existing project, including the ` (archived)` header flag.
func projectShowOutput(slug, name, primary string, archived bool) string {
	flags := ""
	if archived {
		flags = " (archived)"
	}
	out := slug + "  [p_0123abcd]" + flags + "\n" +
		"  name:    " + name + "\n"
	if primary != "" {
		out += "  primary: " + primary + "\n" +
			"  folders:\n" +
			"    * " + primary + "\n"
	}
	return out
}

func okResult(stdout string) execx.Result {
	return execx.Result{ExitCode: 0, Stdout: []byte(stdout)}
}

func exitResult(code int, stdout, stderr string) execx.Result {
	return execx.Result{ExitCode: code, Stdout: []byte(stdout), Stderr: []byte(stderr)}
}

// boolResult is the `test`-command contract: 0 true, 1 false.
func boolResult(ok bool) execx.Result {
	if ok {
		return okResult("")
	}
	return exitResult(1, "", "")
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

func (f *fakeGuest) setFailure(fragment string, exitCode int) {
	f.failContains[fragment] = exitCode
}

var _ Guest = (*fakeGuest)(nil)

// fakeRegistry is the config boundary double. It keeps the persisted document
// in memory so a test can assert exactly what a successful add or remove wrote,
// and can fail the write to exercise the rerunnable-state path.
type fakeRegistry struct {
	file    config.File
	loadErr error
	saveErr error
	saved   []config.File
}

func emptyRegistry() *fakeRegistry {
	return &fakeRegistry{file: config.File{SchemaVersion: config.ConfigSchemaVersion}}
}

func registryWith(projects ...config.Project) *fakeRegistry {
	return &fakeRegistry{file: config.File{SchemaVersion: config.ConfigSchemaVersion, Projects: projects}}
}

func testProject() config.Project {
	return config.Project{ID: testID, DisplayName: testName, Remote: testRemote}
}

func (r *fakeRegistry) Load() (config.File, error) {
	if r.loadErr != nil {
		return config.File{}, r.loadErr
	}
	return r.file, nil
}

func (r *fakeRegistry) Save(f config.File) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.file = f
	r.saved = append(r.saved, f)
	return nil
}

var _ Registry = (*fakeRegistry)(nil)
