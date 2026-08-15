package projects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wzslr321/torio/internal/guestexec"
	"github.com/wzslr321/torio/internal/lima"
)

const (
	// bundleStagingDirName is where a carried bundle lands, private to the
	// identity's own home so the transport's boundary check accepts it.
	bundleStagingDirName = ".torio-project-attach-staging"
	// bundleFileName is what the bundle is called once it is in the guest. It
	// is a constant so no operator-supplied name becomes a guest path: the host
	// file may be called anything, and what it is called there is not something
	// the guest has to agree with.
	bundleFileName = "project.bundle"
	// bundleCleanupTimeout bounds the removal of staging after a transfer, so a
	// cancelled attach still cleans up rather than leaving a repository copy in
	// a directory nobody looks at.
	bundleCleanupTimeout = 30 * time.Second
	// maxBundleBytes bounds what may be carried in one attach. A bundle is a
	// whole repository including its history, so the bound is generous; it
	// exists because an unbounded copy into a guest filesystem is a way to fill
	// it, not because any real repository should approach it.
	maxBundleBytes = 4 << 30
)

func (m *Manager) bundleStagingPath() string {
	return m.identity().Home + "/" + bundleStagingDirName
}

// attachFromBundle carries a Git bundle into the guest and clones the checkout
// out of it (ADR-0027).
//
// A bundle is how a repository with no network remote gets in: Git already has
// a single-file transport for exactly this, the file crosses over the same
// one-shot copy `brain import` uses, and the clone reads it as a source without
// any remote being configured anywhere. Nothing on the host is mounted, the
// guest gains no credential, and the operator's file is read once and never
// referenced again.
//
// The clone is the verification. `git bundle verify` reads the repository it
// runs in and there is none yet — the lesson ADR-0025 learned on a real box —
// so a bundle that is not one fails here, as a clone that could not read its
// source, rather than passing a check that never ran.
func (m *Manager) attachFromBundle(ctx context.Context, op, hostBundle, workspace string) error {
	hostStaging, err := m.stageBundleOnHost(op, hostBundle)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(hostStaging) }()

	staging := m.bundleStagingPath()
	// Cleanup runs even when the attach was cancelled: the staged copy is a
	// whole repository, and leaving one behind in a directory nothing reads is
	// the kind of residue that is only ever found by running out of disk.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bundleCleanupTimeout)
		defer cancel()
		_, _ = m.run(cleanupCtx, op, guestexec.RootExec("rm", "-rf", "--", staging))
	}()

	if err := m.prepareBundleStaging(ctx, op, staging); err != nil {
		return err
	}
	if err := m.guest.CopyToGuest(ctx, hostStaging, staging, m.identity().Home); err != nil {
		return fromGuestErr(op, err)
	}
	// What arrives belongs to the transport, not to the agent: `limactl copy`
	// writes as the guest login identity and carries the host file's mode with
	// it, so the bundle lands owned by somebody else and readable by nobody
	// else — including the identity that is about to clone from it. The
	// directory has the same problem, having taken the private mode of the host
	// staging it came from (ADR-0025).
	//
	// Both are handed to the agent here, in one pass, before anything reads
	// them. Observed on a real box: without this the clone fails as "repository
	// not found", which is what an unreadable file looks like to Git.
	agent := m.identity().GuestUser
	if err := m.mustRun(ctx, op, KindGuestCommand, "hand the carried bundle to the agent",
		guestexec.RootExec("chown", "-R", agent+":"+agent, staging)); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "make the carried bundle readable by the agent",
		guestexec.RootExec("chmod", "-R", "u=rwX,g=,o=", staging)); err != nil {
		return err
	}

	guestBundle := staging + "/" + bundleFileName
	if err := m.mustRun(ctx, op, KindGit, "clone the carried bundle",
		guestexec.UserExecAs(m.identity().GuestUser, "git", "clone", "--quiet", "--", guestBundle, workspace)); err != nil {
		return err
	}
	// A clone from a bundle records the bundle's path as its origin, and that
	// path is about to stop existing. The project is local: it has no origin,
	// and the record says so (ADR-0027).
	return m.mustRun(ctx, op, KindGit, "drop the bundle origin",
		guestexec.UserExecAs(m.identity().GuestUser, "git", "-C", workspace, "remote", "remove", "origin"))
}

// stageBundleOnHost copies the operator's bundle into a private directory of
// its own, because the transport carries a directory and the operator's file
// lives beside whatever else is in theirs.
//
// It also proves the file is a file, and bounded, before anything is carried:
// a directory, a device or a symlink to one is not a bundle, and finding that
// out on the guest would mean finding it out after the copy.
func (m *Manager) stageBundleOnHost(op, hostBundle string) (string, error) {
	if !filepath.IsAbs(hostBundle) {
		return "", &Error{Op: op, Kind: KindInvalidConfig,
			Err: errors.New("the bundle path must be absolute")}
	}
	info, err := os.Lstat(hostBundle)
	if err != nil {
		return "", &Error{Op: op, Kind: KindPrecondition,
			Err: fmt.Errorf("the bundle is not there to carry: %s", filepath.Base(hostBundle))}
	}
	if !info.Mode().IsRegular() {
		return "", &Error{Op: op, Kind: KindPrecondition,
			Err: errors.New("the bundle path is not a regular file")}
	}
	if info.Size() > maxBundleBytes {
		return "", &Error{Op: op, Kind: KindPrecondition,
			Err: fmt.Errorf("the bundle is larger than %d bytes", int64(maxBundleBytes))}
	}

	dir, err := os.MkdirTemp("", "torio-project-bundle-")
	if err != nil {
		return "", &Error{Op: op, Kind: KindTransport, Err: errors.New("private host staging could not be created")}
	}
	if err := copyHostFile(hostBundle, filepath.Join(dir, bundleFileName)); err != nil {
		_ = os.RemoveAll(dir)
		return "", &Error{Op: op, Kind: KindTransport, Err: errors.New("the bundle could not be staged for transfer")}
	}
	return dir, nil
}

// prepareBundleStaging makes the guest directory the transport can actually
// write into.
//
// `limactl copy` is rsync running as the guest login identity, never as the
// agent, so staging owned by the agent at 0700 cannot receive anything: rsync
// stops at "cannot stat destination". The directory therefore belongs to
// whoever the guest session is, with the shared group set so the agent can read
// what lands there (ADR-0025).
func (m *Manager) prepareBundleStaging(ctx context.Context, op, staging string) error {
	if err := m.mustRun(ctx, op, KindGuestCommand, "clean private attach staging",
		guestexec.RootExec("rm", "-rf", "--", staging)); err != nil {
		return err
	}
	transportUser, err := m.guestSessionUser(ctx, op)
	if err != nil {
		return err
	}
	return m.mustRun(ctx, op, KindGuestCommand, "create private attach staging",
		guestexec.RootExec("install", "-d", "-o", transportUser, "-g", lima.TorioProjectsGroup, "-m", "2770", staging))
}

// guestSessionUser reads back the identity guest commands actually run as,
// which is the identity the transport writes as. It is a guest-supplied value
// that becomes an argv element, so it is validated as a login name before use
// rather than trusted because of where it came from.
func (m *Manager) guestSessionUser(ctx context.Context, op string) (string, error) {
	res, err := m.run(ctx, op, []string{"id", "-un"})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", commandError(op, KindGuestCommand, "read the guest session identity", res.ExitCode)
	}
	user := strings.TrimSpace(string(res.Stdout))
	if err := lima.ValidateOperatorUser(user); err != nil {
		return "", &Error{Op: op, Kind: KindVerification,
			Err: errors.New("the guest session identity is not a usable login name")}
	}
	return user, nil
}

// copyHostFile copies one regular file, privately. The destination is created
// 0600 and the directory holding it is the private temporary directory above,
// so the staged copy is no more readable than the operator's own file.
func copyHostFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	// Bounded on the way through as well as by the stat above: the file could
	// have grown, and a copy with no ceiling is a way to fill a disk.
	if _, err := io.Copy(out, io.LimitReader(in, maxBundleBytes)); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
