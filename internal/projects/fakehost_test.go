package projects

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/wzslr321/torio/internal/backend/claudecode"
	"github.com/wzslr321/torio/internal/execx"
)

// The doubles below drive one reconciliation between a guest checkout and the
// bare repository on the host (ADR-0029). Both sides model refs rather than
// commits: what the decision turns on is which ref moves and which does not,
// and a fake that answered ancestry by guessing would prove nothing.

const (
	// testSyncStaging is where the guest writes its bundle and reads the host's,
	// spelled out rather than built from the production helper so a drifting
	// path fails here instead of passing against itself.
	testSyncStaging = claudecode.Home + "/" + syncStagingDirName
	// testBackend is the identity name the host repository namespaces the box's
	// mirror under.
	testBackend = "claude-code"
)

// fakeSyncState is the guest repository as a reconciliation sees it.
type fakeSyncState struct {
	// refs are the checkout's own refs, keyed as the report keys them:
	// "heads/main", "tags/v1".
	refs map[string]string
	// hubRefs are what a fetch from the carried host bundle put under
	// refs/torio/hub. Declared by the test, because the bundle is a file the
	// double never actually reads.
	hubRefs map[string]string
	// ancestor answers `merge-base --is-ancestor A B` for the pairs a test
	// declares, keyed "A B". Every other pair is not an ancestor, which is the
	// answer that stops a ref from moving.
	ancestor map[string]bool
	// counts answers `rev-list --count <range>`; an undeclared range counts 1.
	counts map[string]string

	// mergeBlocked models the tree Git refuses to fast-forward: work in it
	// would be written over, so `merge --ff-only` exits non-zero.
	mergeBlocked bool

	bundled    bool
	carriedOut bool
	carriedIn  bool
	fetched    bool
	// updated and merged record what the reconciliation wrote, in order.
	updated []string
	merged  []string
}

func (s *fakeSyncState) route(joined string) (execx.Result, bool) {
	const ws = "git -C " + testPath + " "
	switch {
	case strings.Contains(joined, "rm -rf -- "+testSyncStaging),
		strings.Contains(joined, "install -d -o "+testOwner+" -g "+sharedGroup+" -m 2770 "+testSyncStaging),
		strings.Contains(joined, "chmod 2770 "+testSyncStaging):
		return okResult(""), true

	case strings.Contains(joined, ws+"for-each-ref --count=1"):
		for _, name := range sortedKeys(s.refs) {
			return okResult("refs/" + name + "\n"), true
		}
		return okResult(""), true

	case strings.Contains(joined, ws+"for-each-ref") && strings.Contains(joined, hubMirrorRef):
		return okResult(formatRefs(s.hubRefs, hubMirrorRef+"/")), true
	case strings.Contains(joined, ws+"for-each-ref"):
		return okResult(formatRefs(s.refs, "refs/")), true

	case strings.Contains(joined, ws+"bundle create "+testSyncStaging+"/"+guestBundleName+" --branches --tags"):
		s.bundled = true
		return okResult(""), true

	case strings.Contains(joined, ws+"fetch --quiet --prune --no-tags "+testSyncStaging+"/"+hubBundleName):
		if !s.carriedIn {
			return exitResult(128, "", "fatal: could not read bundle"), true
		}
		s.fetched = true
		return okResult(""), true

	case strings.Contains(joined, ws+"merge-base --is-ancestor "):
		_, pair, _ := strings.Cut(joined, "--is-ancestor ")
		return boolResult(s.ancestor[strings.TrimSpace(pair)]), true

	case strings.Contains(joined, ws+"update-ref "):
		_, rest, _ := strings.Cut(joined, "update-ref ")
		s.updated = append(s.updated, strings.TrimSpace(rest))
		fields := strings.Fields(rest)
		s.refs[strings.TrimPrefix(fields[0], "refs/")] = fields[1]
		return okResult(""), true

	case strings.Contains(joined, ws+"merge --ff-only "):
		if s.mergeBlocked {
			return exitResult(1, "", "error: Your local changes to the following files would be overwritten by merge"), true
		}
		_, ref, _ := strings.Cut(joined, "--ff-only ")
		s.merged = append(s.merged, strings.TrimSpace(ref))
		return okResult(""), true

	case strings.Contains(joined, ws+"rev-list --count "):
		_, arg, _ := strings.Cut(joined, "--count ")
		return okResult(countOf(s.counts, strings.TrimSpace(arg))), true
	}
	return execx.Result{}, false
}

// fakeHostGit is the bare repository on the host. Only Git runs against it, so
// the double is a router over one argv, exactly as the production seam is.
type fakeHostGit struct {
	calls []string
	// refs is what the bare repository holds, mirror what a fetch from the
	// guest's bundle wrote under refs/torio/<backend>.
	refs     map[string]string
	mirror   map[string]string
	ancestor map[string]bool
	counts   map[string]string

	// headResolves is whether HEAD names a branch the repository has. A freshly
	// initialized bare repository does not, which is why a clone from it needs
	// somebody to point HEAD somewhere first.
	headResolves  bool
	headPointedAt string

	initialized bool
	bundled     bool
	fetchFails  bool
	updated     []string
}

func newFakeHostGit() *fakeHostGit {
	return &fakeHostGit{
		refs:     map[string]string{},
		mirror:   map[string]string{},
		ancestor: map[string]bool{},
		counts:   map[string]string{},
	}
}

func (f *fakeHostGit) Run(_ context.Context, argv []string) (execx.Result, error) {
	joined := strings.Join(argv, " ")
	f.calls = append(f.calls, joined)
	mirror := "refs/torio/" + testBackend
	switch {
	case strings.Contains(joined, "init --bare"):
		f.initialized = true
		return execx.Result{}, nil

	case strings.Contains(joined, "rev-parse --verify --quiet HEAD"):
		return boolResult(f.headResolves), nil
	case strings.Contains(joined, "symbolic-ref HEAD "):
		_, ref, _ := strings.Cut(joined, "symbolic-ref HEAD ")
		f.headPointedAt = strings.TrimSpace(ref)
		f.headResolves = true
		return execx.Result{}, nil

	case strings.Contains(joined, "fetch --quiet --prune"):
		if f.fetchFails {
			return execx.Result{ExitCode: 128}, nil
		}
		return execx.Result{}, nil

	case strings.Contains(joined, "for-each-ref") && strings.Contains(joined, mirror):
		return okResult(formatRefs(f.mirror, mirror+"/")), nil
	case strings.Contains(joined, "for-each-ref"):
		return okResult(formatRefs(f.refs, "refs/")), nil

	case strings.Contains(joined, "bundle create"):
		f.bundled = true
		// The file has to land: the attach that reads it proves it is a regular
		// file before anything is carried.
		_, rest, _ := strings.Cut(joined, "bundle create ")
		if path, _, ok := strings.Cut(rest, " "); ok {
			if err := os.WriteFile(path, []byte("bundle\n"), 0o600); err != nil {
				return execx.Result{ExitCode: 1}, nil
			}
		}
		return execx.Result{}, nil

	case strings.Contains(joined, "merge-base --is-ancestor "):
		_, pair, _ := strings.Cut(joined, "--is-ancestor ")
		return boolResult(f.ancestor[strings.TrimSpace(pair)]), nil

	case strings.Contains(joined, "update-ref "):
		_, rest, _ := strings.Cut(joined, "update-ref ")
		f.updated = append(f.updated, strings.TrimSpace(rest))
		fields := strings.Fields(rest)
		f.refs[strings.TrimPrefix(fields[0], "refs/")] = fields[1]
		return execx.Result{}, nil

	case strings.Contains(joined, "rev-list --count "):
		_, arg, _ := strings.Cut(joined, "--count ")
		return okResult(countOf(f.counts, strings.TrimSpace(arg))), nil
	}
	return execx.Result{}, nil
}

func (f *fakeHostGit) saw(needle string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, needle) {
			return true
		}
	}
	return false
}

// formatRefs renders a ref map the way `for-each-ref` prints it, sorted so an
// assertion on the order of what a sync did is stable.
func formatRefs(refs map[string]string, prefix string) string {
	var b strings.Builder
	for _, name := range sortedKeys(refs) {
		b.WriteString(prefix + name + " " + refs[name] + "\n")
	}
	return b.String()
}

func sortedKeys(refs map[string]string) []string {
	out := make([]string, 0, len(refs))
	for name := range refs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func countOf(counts map[string]string, arg string) string {
	if n, ok := counts[arg]; ok {
		return n + "\n"
	}
	return "1\n"
}
