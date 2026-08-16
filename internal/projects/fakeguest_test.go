package projects

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/wzslr321/torio/internal/backend/claudecode"
	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

// The fixtures below describe one project. They are the values every test
// asserts against, so a drifting derived path or remote fails loudly.
const (
	testID     = "demo"
	testName   = "Demo"
	testRemote = "git@github.com:owner/demo.git"
	testHost   = "github.com"
	testPath   = claudecode.WorkspacePath + "/" + testID
	testOwner  = "operator"
	// testKeyPath and testKeyConfig are the deploy key and the Git include file
	// derived from the identity home and the project ID. They are spelled out
	// rather than built from the production helpers so a drifting path fails
	// here instead of passing against itself.
	testKeyPath   = claudecode.Home + "/.ssh/torio/" + testID
	testKeyConfig = testKeyPath + ".gitconfig"
	// testBundleStaging is where a carried bundle lands, spelled out for the
	// same reason as the key paths above.
	testBundleStaging = claudecode.Home + "/" + bundleStagingDirName
	// testPublicKey is the public half the guest double reports. A public key is
	// not a credential: it is the half an operator is meant to publish.
	testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeFakeFakeFakeFakeFakeFakeFakeFakeFake torio-deploy-" + testID
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

// unresolvableHostStderr is what a guest answers when the recorded host is a
// name only the operator's machine knows. Read from a codex guest on
// 2026-08-15 against a remote carrying a host-local SSH alias.
const unresolvableHostStderr = "ssh: Could not resolve hostname gh-demo: Temporary failure in name resolution\n" +
	"fatal: Could not read from remote repository."

type fakeCall struct {
	argv []string
}

// fakeGuest is the typed guest double. It routes on argv rather than on call
// order, so a test that adds a probe somewhere in the middle does not have to
// renumber a script — and a command nothing routes is an error rather than a
// silent empty reply.
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
	// branch is what `symbolic-ref --short HEAD` prints; empty models a
	// detached HEAD. ahead is what `rev-list --count @{u}..HEAD` prints; empty
	// models a branch with no upstream configured.
	branch string
	ahead  string
	// pushURL is what `git remote get-url --push origin` prints, and knownHosts
	// is the set of hosts each guest identity trusts. They are separate because
	// the two session shapes read different home directories, which is the bug
	// RemoteAccess exists to catch.
	pushURL    string
	knownHosts map[string][]string
	credHelper bool
	owner      string
	group      string
	mode       string

	// remote is the URL the guest holds and serves; remoteReadable decides
	// whether the noninteractive preflight can read it.
	remote         string
	remoteReadable bool
	// remoteUnresolvable models a host the guest has no way to reach at all,
	// which is a different answer from a remote it may not read.
	remoteUnresolvable bool

	// deployKeyExists is the guest-side deploy key at testKeyPath, and
	// keyAuthorized is whether the forge accepts it. They are separate because
	// the interesting case is exactly the gap between them: a key Torio has
	// generated but nobody has authorized yet reads no better than none.
	deployKeyExists bool
	keyAuthorized   bool

	// Per-user global safe.directory entries.
	safeDirs map[string][]string

	// The bundle attach: what was carried, whether the carry failed, and
	// whether a bundle is sitting in staging for the clone to read.
	copies        []fakeCopy
	copyErr       error
	bundleArrived bool
	// bundleReadable is whether the carried bundle has been handed to the
	// agent. The transport writes as the login identity, so a bundle that
	// arrived is not yet a bundle the agent can open.
	bundleReadable bool

	failContains map[string]int
	truncateOn   string
	calls        []fakeCall
}

// fakeCopy is one host→guest transfer the manager asked for.
type fakeCopy struct {
	host  string
	guest string
	home  string
}

// readyFake is a bootstrap-verified guest with a reachable remote and no
// checkout: the shape `Add` clones into.
func readyFake() *fakeGuest {
	return &fakeGuest{
		remote:         testRemote,
		remoteReadable: true,
		owner:          claudecode.User,
		group:          sharedGroup,
		mode:           "2775",
		safeDirs:       map[string][]string{},
		failContains:   map[string]int{},
	}
}

// attachedFake is a guest that already holds a compliant checkout: the shape a
// rerun of `Add` must accept unchanged.
func attachedFake() *fakeGuest {
	f := readyFake()
	f.pathExists = true
	f.isRepo = true
	f.origin = f.remote
	f.safeDirs[claudecode.User] = []string{testPath}
	f.safeDirs[testOwner] = []string{testPath}
	f.branch = "main"
	f.ahead = "3"
	f.pushURL = f.remote
	f.knownHosts = map[string][]string{}
	return f
}

func (f *fakeGuest) Bootstrap(context.Context, lima.BootstrapOptions) (lima.BootstrapReport, error) {
	if f.bootstrapErr != nil {
		return lima.BootstrapReport{}, f.bootstrapErr
	}
	return lima.BootstrapReport{Instance: lima.InstanceName}, nil
}

// CopyToGuest records the one carry a bundle attach makes. The real transport
// is rsync as the login identity; what a test can assert is that the manager
// asked for the right directories, within the identity's own home, and that a
// failure crosses the boundary as one.
func (f *fakeGuest) CopyToGuest(_ context.Context, hostDir, guestDir, guestHome string) error {
	f.copies = append(f.copies, fakeCopy{host: hostDir, guest: guestDir, home: guestHome})
	if f.copyErr != nil {
		return f.copyErr
	}
	f.bundleArrived = true
	return nil
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

	// --- guest-held deploy key ---
	case strings.Contains(joined, "test -f "+testKeyPath):
		return boolResult(f.deployKeyExists), nil
	case strings.Contains(joined, "mkdir -p -m 0700 "):
		return okResult(""), nil
	case strings.Contains(joined, "ssh-keygen -q -t ed25519"):
		f.deployKeyExists = true
		return okResult(""), nil
	case strings.Contains(joined, "cat "+testKeyPath+".pub"):
		if !f.deployKeyExists {
			return exitResult(1, "", "cat: No such file or directory"), nil
		}
		return okResult(testPublicKey + "\n"), nil
	case strings.Contains(joined, "git config -f "+testKeyConfig+" core.sshCommand"):
		return okResult(""), nil
	case strings.Contains(joined, "git config --global includeIf.gitdir:"+testPath+"/.path"):
		return okResult(""), nil

	// --- local projects: an empty repository, or one carried in ---
	case strings.Contains(joined, "git init --initial-branch=main "+testPath):
		f.pathExists = true
		f.isRepo = true
		f.origin = ""
		f.owner = claudecode.User
		f.group = claudecode.User
		f.mode = "755"
		return okResult(""), nil
	case strings.Contains(joined, "id -un"):
		return okResult(testOwner + "\n"), nil
	case strings.Contains(joined, "rm -rf -- "+testBundleStaging):
		f.bundleArrived, f.bundleReadable = false, false
		return okResult(""), nil
	case strings.Contains(joined, "install -d -o "+testOwner+" -g "+sharedGroup+" -m 2770 "+testBundleStaging):
		return okResult(""), nil
	case strings.Contains(joined, "chown -R "+claudecode.User+":"+claudecode.User+" "+testBundleStaging):
		f.bundleReadable = true
		return okResult(""), nil
	case strings.Contains(joined, "chmod -R u=rwX,g=,o= "+testBundleStaging):
		return okResult(""), nil
	case strings.Contains(joined, "git clone --quiet -- "+testBundleStaging+"/project.bundle "+testPath):
		// A bundle that arrived but was never handed to the agent is a file it
		// cannot read, which is what Git reports as a missing repository. This
		// is the real-box failure the adopt step exists to prevent.
		if !f.bundleArrived || !f.bundleReadable {
			return exitResult(128, "", "fatal: repository not found"), nil
		}
		f.pathExists = true
		f.isRepo = true
		// A clone from a bundle records the bundle path as its origin, which is
		// exactly what the attach has to remove.
		f.origin = testBundleStaging + "/project.bundle"
		f.owner = claudecode.User
		f.group = claudecode.User
		f.mode = "755"
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+testPath+" remote remove origin"):
		f.origin = ""
		return okResult(""), nil

	// --- correcting the recorded remote, or attaching one to a local project ---
	case strings.Contains(joined, "git -C "+testPath+" remote add origin "):
		_, url, _ := strings.Cut(joined, "remote add origin ")
		f.origin = strings.TrimSpace(url)
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+testPath+" remote set-url origin "):
		_, url, _ := strings.Cut(joined, "remote set-url origin ")
		f.origin = strings.TrimSpace(url)
		return okResult(""), nil

	// --- remote preflight and clone ---
	case strings.Contains(joined, "git ls-remote -- "+f.remote+" HEAD"):
		if f.remoteUnresolvable {
			return exitResult(128, "", unresolvableHostStderr), nil
		}
		if !f.canReadRemote(joined) {
			return exitResult(128, "", remoteFailureStderr), nil
		}
		return okResult("0123456789abcdef\tHEAD\n"), nil
	case strings.Contains(joined, "git clone --quiet -- "+f.remote+" "+testPath):
		if !f.canReadRemote(joined) {
			return exitResult(128, "", remoteFailureStderr), nil
		}
		f.pathExists = true
		f.isRepo = true
		f.origin = f.remote
		f.owner = claudecode.User
		f.group = claudecode.User
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
	case strings.Contains(joined, "git -C "+testPath+" remote get-url --push origin"):
		if f.pushURL == "" {
			return exitResult(1, "", ""), nil
		}
		return okResult(f.pushURL + "\n"), nil
	case strings.Contains(joined, "ssh-keygen -F "):
		user := userOf(joined)
		host := joined[strings.LastIndex(joined, " ")+1:]
		for _, known := range f.knownHosts[user] {
			if known == host {
				return okResult("# Host " + host + " found: line 1\n"), nil
			}
		}
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "git -C "+testPath+" symbolic-ref --short HEAD"):
		if f.branch == "" {
			return exitResult(128, "", "fatal: ref HEAD is not a symbolic ref"), nil
		}
		return okResult(f.branch + "\n"), nil
	case strings.Contains(joined, "git -C "+testPath+" rev-list --count @{u}..HEAD"):
		if f.ahead == "" {
			return exitResult(128, "", "fatal: no upstream configured for branch"), nil
		}
		return okResult(f.ahead + "\n"), nil
	case strings.Contains(joined, "git -C "+testPath+" config --local --get-regexp"):
		if f.credHelper {
			return okResult("credential.helper store\n"), nil
		}
		return exitResult(1, "", ""), nil

	// --- shared permissions ---
	case strings.Contains(joined, "chown -R "+claudecode.User+":"+sharedGroup+" -- "+testPath):
		f.owner = claudecode.User
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

	}
	return execx.Result{}, fmt.Errorf("unrouted fake guest command: %s", joined)
}

// canReadRemote decides whether a remote operation succeeds. A guest reaches a
// remote either because it can already read it, or because this argv offers the
// deploy key and the forge has been told to accept it. Offering a key nobody
// authorized reads no better than offering none, which is the state a first run
// leaves behind.
//
// Offering the key without IdentitiesOnly does not read either, which is the
// forge behaviour the real failure comes from: ssh presents every identity it
// holds, the forge authenticates the first one valid for the account rather
// than the one valid for this repository, and answers `Repository not found`
// for a repository that exists. Modelling it here is what makes the option
// load-bearing in the suite instead of only in a comment.
func (f *fakeGuest) canReadRemote(joined string) bool {
	if f.remoteReadable {
		return true
	}
	if !f.keyAuthorized || !strings.Contains(joined, "-i "+testKeyPath) {
		return false
	}
	return strings.Contains(joined, "-o IdentitiesOnly=yes")
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

func (r *fakeRegistry) Update(update func(config.File) (config.File, error)) error {
	f, err := r.Load()
	if err != nil {
		return err
	}
	next, err := update(f)
	if err != nil {
		return err
	}
	if slices.Equal(f.Projects, next.Projects) {
		return nil
	}
	return r.Save(next)
}

var _ Registry = (*fakeRegistry)(nil)
