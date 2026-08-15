package brain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

// fakeGuestSessionUser is the identity `id -un` reports on the fake guest: the
// Lima login user, which is who `limactl copy` writes as.
const fakeGuestSessionUser = "operator"

type fakeCall struct {
	argv  []string
	stdin []byte
}

type fakeCopy struct {
	direction string
	host      string
	guest     string
	// home is the boundary the transfer declared. The real transport refuses a
	// destination outside it, so a test that never looked at it would pass
	// while the product failed on the guest.
	home string
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
	skillDirMode    string
	skillDirSymlink bool
	skillSymlink    bool
	skillPresent    bool
	skillDigest     string
	skillOwner      string
	skillGroup      string
	skillMode       string
	skillStaged     []byte
	// Category state mirrors the skill's: the DESCRIPTION.md is installed,
	// digested and ownership-checked exactly like SKILL.md.
	categoryDirMode string
	categoryPresent bool
	categoryDigest  string
	categoryMode    string
	// legacySkillPresent models a guest installed before the category move.
	legacySkillPresent bool
	failContains       map[string]int
	truncateOn         string
	transportErr       error
	calls              []fakeCall
	copies             []fakeCopy
	copyFromErr        error
	// carriedBundle is the file name CopyFromGuest writes into the host staging
	// directory, empty for a transport that carries nothing.
	carriedBundle string
	copyToErr     error
	// behindHub is what `rev-list --count main..refs/torio/hub` prints, and
	// guestMergeConflicts makes the inbound merge report a conflict.
	behindHub           string
	aheadOfHub          string
	hubRefKnown         bool
	syncStagingShared   bool
	guestMergeConflicts bool

	importFiles    int
	privateExists  map[string]bool
	pristineTree   bool
	exchangedEmpty bool
	cancel         context.CancelFunc
	cancelOn       string
	failExchangeAt int
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
		importFiles:  2,
		failContains: map[string]int{},
		behindHub:    "0",
		aheadOfHub:   "0",
	}
}

func initializedFake() *fakeGuest {
	f := readyFake()
	f.empty = false
	f.scaffold = true
	f.gitRepo = true
	f.registered = true
	f.markdownCount = 3
	f.pristineTree = true
	return f
}

// The payloads this package's tests are written against. They are the Hermes
// backend's own declaration, reached the way production reaches it, because the
// retrieval skill now travels with the backend rather than living here — and a
// test that kept its own copy would pass while the shipped one drifted.
func hermesSkillPayload() ([]byte, string, error) {
	return declaredPayload(lima.Hermes().BrainSkill().Payload)
}

func hermesCategoryPayload() ([]byte, string, error) {
	return declaredPayload(lima.Hermes().BrainSkill().CategoryPayload)
}

// withInstalledSkill puts the current embedded payload on the fake guest with
// the ownership and mode a real install produces.
func (f *fakeGuest) withInstalledSkill(t *testing.T) *fakeGuest {
	t.Helper()
	_, digest, err := hermesSkillPayload()
	if err != nil {
		t.Fatalf("hermesSkillPayload() error = %v", err)
	}
	_, categoryDigest, err := hermesCategoryPayload()
	if err != nil {
		t.Fatalf("hermesCategoryPayload() error = %v", err)
	}
	f.skillDirMode = "750"
	f.skillPresent = true
	f.skillDigest = digest
	f.skillOwner = lima.HermesUser
	f.skillGroup = lima.HermesUser
	f.skillMode = "640"
	// A current installation includes the category description; without it the
	// skill renders at the top level with no uncapped description, which is the
	// state the category move exists to leave behind.
	f.categoryDirMode = "750"
	f.categoryPresent = true
	f.categoryDigest = categoryDigest
	f.categoryMode = "640"
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

func (f *fakeGuest) CopyToGuest(_ context.Context, hostSourceDir, guestDestinationDir, guestHome string) error {
	f.copies = append(f.copies, fakeCopy{direction: "to_guest", host: hostSourceDir, guest: guestDestinationDir, home: guestHome})
	return f.copyToErr
}

// CopyFromGuest records the direction and, when a test asked for one, writes
// the bundle the host side then reads. Writing a real file matters: the sync
// checks that what it asked for actually arrived.
func (f *fakeGuest) CopyFromGuest(_ context.Context, guestSourceDir, hostDestinationDir, guestHome string) error {
	f.copies = append(f.copies, fakeCopy{direction: "from_guest", host: hostDestinationDir, guest: guestSourceDir, home: guestHome})
	if f.copyFromErr != nil {
		return f.copyFromErr
	}
	if f.carriedBundle == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(hostDestinationDir, f.carriedBundle), []byte("bundle"), 0o600)
}

func (f *fakeGuest) route(ctx context.Context, stdin []byte, argv []string) (execx.Result, error) {
	if err := ctx.Err(); err != nil {
		return execx.Result{ExitCode: -1}, err
	}
	f.calls = append(f.calls, fakeCall{argv: append([]string(nil), argv...), stdin: append([]byte(nil), stdin...)})
	if f.transportErr != nil {
		return execx.Result{ExitCode: -1}, f.transportErr
	}
	joined := strings.Join(argv, " ")
	if f.cancelOn != "" && strings.Contains(joined, f.cancelOn) {
		f.cancelOn = ""
		f.cancel()
		return execx.Result{ExitCode: -1}, ctx.Err()
	}
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

	// --- reconciliation with the hub vault (ADR-0025) ---
	case strings.Contains(joined, "install -d -o "+fakeGuestSessionUser+" -g "+lima.TorioProjectsGroup+" -m 2770 "+syncStagingPath):
		return okResult(""), nil
	case strings.Contains(joined, "rm -rf -- "+syncStagingPath):
		return okResult(""), nil
	case strings.Contains(joined, "chmod 2770 "+syncStagingPath):
		f.syncStagingShared = true
		return okResult(""), nil
	case strings.Contains(joined, "bundle create"):
		return okResult(""), nil
	case strings.Contains(joined, "bundle verify"):
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+Path) && strings.Contains(joined, "add -A"):
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+Path) && strings.Contains(joined, " commit -q -m "+syncCommitMessage):
		f.gitDirty = false
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+Path+" fetch --quiet"):
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+Path+" rev-parse --verify --quiet "+hubRef):
		if !f.hubRefKnown {
			return exitResult(1, "", ""), nil
		}
		return okResult("0123456789abcdef\n"), nil
	case strings.Contains(joined, "git -C "+Path+" rev-list --count "+hubBranch+".."+hubRef):
		return okResult(f.behindHub + "\n"), nil
	case strings.Contains(joined, "git -C "+Path+" rev-list --count "+hubRef+".."+hubBranch):
		return okResult(f.aheadOfHub + "\n"), nil
	case strings.Contains(joined, "git -C "+Path) && strings.Contains(joined, "merge --abort"):
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+Path) && strings.Contains(joined, "merge --no-edit"):
		if f.guestMergeConflicts {
			return exitResult(1, "", "CONFLICT"), nil
		}
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
		// Private Brain import staging and candidate routes.
	case strings.Contains(joined, "rm -rf -- "+importStagingPath):
		return okResult(""), nil
	case strings.Contains(joined, "dd of="+importManifestPath):
		return okResult(""), nil
	case strings.Contains(joined, "find "+importPayloadPath+" -type l"):
		return okResult(""), nil
	case strings.Contains(joined, "find "+importPayloadPath+" ! -type d ! -type f"):
		return okResult(""), nil
	case strings.Contains(joined, "find "+importPayloadPath+" -type f"):
		return okResult(strings.Repeat(".", f.importFiles)), nil
	case joined == "id -un":
		return okResult(fakeGuestSessionUser + "\n"), nil
	case strings.Contains(joined, "chown -R -- "+lima.HermesUser+":"+lima.HermesUser+" "+importPayloadPath):
		return okResult(""), nil
	case strings.Contains(joined, "chmod -R u=rwX,g=rX,o= -- "+importPayloadPath):
		return okResult(""), nil
	case strings.Contains(joined, "sha256sum --quiet --strict -c "+importManifestPath):
		return okResult(""), nil
	case strings.Contains(joined, "mv -T "+importPayloadPath+" "+importCandidatePath):
		return okResult(""), nil
	case strings.Contains(joined, "cp -a -- "+Path+"/. "+importCandidatePath+"/"):
		return okResult(""), nil
	case strings.Contains(joined, "cp -a --update=none-fail -- "+importPayloadPath+"/. "+importCandidatePath+"/"):
		return okResult(""), nil
	case strings.Contains(joined, "cp -a -- "+importPayloadPath+"/. "+importCandidatePath+"/"):
		return okResult(""), nil
	case strings.Contains(joined, "python3 -c ") && strings.Contains(joined, "renameat2"):
		if f.failExchangeAt > 0 && f.count("renameat2") == f.failExchangeAt {
			return exitResult(1, "", "synthetic exchange failure"), nil
		}
		if f.empty {
			f.empty = false
			f.scaffold = true
			f.gitRepo = true
			f.exchangedEmpty = true
		} else if f.exchangedEmpty {
			f.empty = true
			f.scaffold = false
			f.gitRepo = false
			f.exchangedEmpty = false
		}
		return okResult(""), nil
	case strings.Contains(joined, "test -f "+importCandidatePath+"/"):
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "tee "+importCandidatePath+"/"):
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+importCandidatePath+" rev-parse --verify HEAD"):
		return okResult("0123456789abcdef\n"), nil
	case strings.Contains(joined, "git -C "+importCandidatePath+" remote"):
		return okResult(""), nil
	case strings.Contains(joined, "git -C "+importCandidatePath+" init"),
		strings.Contains(joined, "git -C "+importCandidatePath+" add -A"),
		strings.Contains(joined, "git -C "+importCandidatePath+" -c user.name="):
		return okResult(""), nil
	case strings.Contains(joined, "mv -T "+importCandidatePath+" "+Path):
		f.pathExists = true
		f.empty = false
		f.scaffold = true
		f.gitRepo = true
		return okResult(""), nil
	case strings.Contains(joined, "test -e "+Path+"/"):
		privatePath := argv[len(argv)-1]
		if f.privateExists[privatePath] {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
	// Retrieval skill routes. SkillFilePath has SkillPath as a prefix, so the
	// file probes must be matched first.
	case strings.Contains(joined, "test -L "+SkillFilePath):
		if f.skillSymlink {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "test -f "+SkillFilePath):
		if f.skillPresent {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "stat -c %U:%G %a "+SkillFilePath):
		if !f.skillPresent {
			return exitResult(1, "", "no such file or directory"), nil
		}
		return okResult(f.skillOwner + ":" + f.skillGroup + " " + f.skillMode + "\n"), nil
	case strings.Contains(joined, "sha256sum -- "+SkillFilePath):
		if !f.skillPresent {
			return exitResult(1, "", "no such file or directory"), nil
		}
		return okResult(f.skillDigest + "  " + SkillFilePath + "\n"), nil
	case strings.Contains(joined, "test -L "+SkillPath):
		if f.skillDirSymlink {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "test -d "+SkillPath):
		if f.skillDirMode != "" {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "stat -c %U:%G %a "+SkillPath):
		if f.skillDirMode == "" {
			return exitResult(1, "", "no such file or directory"), nil
		}
		return okResult(lima.HermesUser + ":" + lima.HermesUser + " " + f.skillDirMode + "\n"), nil
	case strings.Contains(joined, "install -d -o hermes -g hermes -m 0750 "+SkillPath):
		f.skillDirMode = "750"
		return okResult(""), nil
	// Category routes. SkillCategoryPath is a prefix of both SkillPath and
	// SkillCategoryFilePath, so it must be matched after them.
	case strings.Contains(joined, "test -L "+SkillCategoryFilePath):
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "test -f "+SkillCategoryFilePath):
		if f.categoryPresent {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "stat -c %U:%G %a "+SkillCategoryFilePath):
		if !f.categoryPresent {
			return exitResult(1, "", "no such file or directory"), nil
		}
		return okResult(lima.HermesUser + ":" + lima.HermesUser + " " + f.categoryMode + "\n"), nil
	case strings.Contains(joined, "sha256sum -- "+SkillCategoryFilePath):
		if !f.categoryPresent {
			return exitResult(1, "", "no such file or directory"), nil
		}
		return okResult(f.categoryDigest + "  " + SkillCategoryFilePath + "\n"), nil
	case strings.Contains(joined, "mv -T "+skillStagingPath+" "+SkillCategoryFilePath):
		sum := sha256.Sum256(f.skillStaged)
		f.categoryPresent = true
		f.categoryDigest = hex.EncodeToString(sum[:])
		f.categoryMode = "640"
		f.skillStaged = nil
		return okResult(""), nil
	case strings.Contains(joined, "test -L "+SkillCategoryPath):
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "test -d "+SkillCategoryPath):
		if f.categoryDirMode != "" {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "stat -c %U:%G %a "+SkillCategoryPath):
		if f.categoryDirMode == "" {
			return exitResult(1, "", "no such file or directory"), nil
		}
		return okResult(lima.HermesUser + ":" + lima.HermesUser + " " + f.categoryDirMode + "\n"), nil
	case strings.Contains(joined, "install -d -o hermes -g hermes -m 0750 "+SkillCategoryPath):
		f.categoryDirMode = "750"
		return okResult(""), nil
	// Pre-category installation, retired by removeLegacySkill.
	case strings.Contains(joined, "test -L "+legacySkillPath):
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "test -f "+legacySkillPath+"/SKILL.md"),
		strings.Contains(joined, "test -d "+legacySkillPath):
		if f.legacySkillPresent {
			return okResult(""), nil
		}
		return exitResult(1, "", ""), nil
	case strings.Contains(joined, "rm -f -- "+legacySkillPath+"/SKILL.md"):
		f.legacySkillPresent = false
		return okResult(""), nil
	case strings.Contains(joined, "rmdir -- "+legacySkillPath):
		return okResult(""), nil
	case strings.Contains(joined, "tee "+skillStagingPath):
		f.skillStaged = append([]byte(nil), stdin...)
		return okResult(""), nil
	case strings.Contains(joined, "chmod 0640 "+skillStagingPath):
		return okResult(""), nil
	case strings.Contains(joined, "mv -T "+skillStagingPath+" "+SkillFilePath):
		sum := sha256.Sum256(f.skillStaged)
		f.skillPresent = true
		f.skillSymlink = false
		f.skillDigest = hex.EncodeToString(sum[:])
		f.skillOwner = lima.HermesUser
		f.skillGroup = lima.HermesUser
		f.skillMode = "640"
		f.skillStaged = nil
		return okResult(""), nil
	case strings.Contains(joined, "rm -f -- "+skillStagingPath):
		f.skillStaged = nil
		return okResult(""), nil
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
	case strings.Contains(joined, "git -C "+Path+" rev-list --count HEAD"):
		return okResult("1\n"), nil
	case strings.Contains(joined, "git -C "+Path+" ls-tree -r --name-only HEAD"):
		if f.pristineTree {
			return okResult("AGENTS.md\nREADME.md\ntodo.md\n"), nil
		}
		return okResult("AGENTS.md\nREADME.md\nprivate.md\ntodo.md\n"), nil
	case strings.Contains(joined, "sha256sum -- "+Path+"/"):
		name := strings.TrimPrefix(argv[len(argv)-1], Path+"/")
		payload, err := scaffoldFS.ReadFile("templates/" + name)
		if err != nil {
			return exitResult(1, "", "not a scaffold file"), nil
		}
		sum := sha256.Sum256(payload)
		return okResult(hex.EncodeToString(sum[:]) + "  " + Path + "/" + name + "\n"), nil
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

func (f *fakeGuest) firstIndex(fragment string) int {
	for i, call := range f.calls {
		if strings.Contains(strings.Join(call.argv, " "), fragment) {
			return i
		}
	}
	return -1
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

func (g *blockingGuest) CopyToGuest(ctx context.Context, hostSourceDir, guestDestinationDir, guestHome string) error {
	return g.base.CopyToGuest(ctx, hostSourceDir, guestDestinationDir, guestHome)
}

func (g *blockingGuest) CopyFromGuest(ctx context.Context, guestSourceDir, hostDestinationDir, guestHome string) error {
	return g.base.CopyFromGuest(ctx, guestSourceDir, hostDestinationDir, guestHome)
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
