package lima

import (
	"context"
	"fmt"
	"os"
)

// Init creates the Torio VM from the embedded, Gate-0-pinned template, or
// succeeds idempotently when an existing instance already matches the trusted
// security pins (image digest/URL, empty mounts, forwardAgent=false, and the
// host profile's driver and architecture).
//
// It never recreates, resets, deletes, or force-overwrites an incompatible
// instance — that fails closed as KindIncompatible. There is no --force.
//
// Create uses the exact promoted argv shape:
//
//	limactl create --name=torio --tty=false <private-temp-template.yaml>
//
// The private template is fsynced, passed to limactl, then unlinked even on
// failure.
func (a *Adapter) Init(ctx context.Context, opts InitOptions) (InitResult, error) {
	const op = "init"
	var out InitResult

	// Resolved before anything else: an unsupported host must not reach a
	// `limactl create`, and the reported image pin has to be the one this host
	// would actually use rather than a default that happens to be compiled in.
	profile, err := a.profile()
	if err != nil {
		return out, &Error{Op: op, Kind: KindVerificationFailed, Err: err}
	}
	out.ImageLocation = profile.ImageURL
	out.ImageDigest = profile.ImageDigest

	opts = opts.withDefaults()
	if err := validateOperatorUser(opts.OperatorUser); err != nil {
		return out, &Error{Op: op, Kind: KindVerificationFailed, Err: err}
	}

	rec, err := a.currentInstance(ctx, op)
	if err != nil {
		return out, err
	}
	if rec != nil {
		if err := verifyCompatibleConfig(rec, profile); err != nil {
			return out, &Error{Op: op, Kind: KindIncompatible, Err: err}
		}
		out.Created = false
		return out, nil
	}

	body, err := renderTemplate(opts, profile)
	if err != nil {
		return out, &Error{Op: op, Kind: KindVerificationFailed, Err: err}
	}
	path, err := writePrivateTemplate(body)
	if err != nil {
		return out, &Error{Op: op, Kind: KindVerificationFailed, Err: fmt.Errorf("write template: %w", err)}
	}
	defer func() { _ = os.Remove(path) }()

	// --tty=false must precede the template path.
	res, err := a.runRaw(ctx, "create", "--name="+InstanceName, "--tty=false", path)
	if err != nil {
		return out, classifyRunErr(op, err)
	}
	if res.ExitCode != 0 {
		return out, commandFailed(op, res.ExitCode, res.Stderr)
	}

	rec, err = a.currentInstance(ctx, op)
	if err != nil {
		return out, err
	}
	if rec == nil {
		return out, &Error{Op: op, Kind: KindPostconditionFailed, Err: fmt.Errorf("instance %q not found after create", InstanceName)}
	}
	if err := verifyCompatibleConfig(rec, profile); err != nil {
		return out, &Error{Op: op, Kind: KindPostconditionFailed, Err: err}
	}

	out.Created = true
	return out, nil
}

// verifyCompatibleConfig answers the one question the pins exist for: is this
// instance the one Torio would have created on this host? Every expectation
// comes from the profile that rendered the template, so the two can disagree
// about an instance only if this function stops reading the same struct.
func verifyCompatibleConfig(rec *instanceRecord, profile Profile) error {
	if !profile.valid() {
		return fmt.Errorf("no instance pins to verify against")
	}
	cfg := rec.Config
	if cfg == nil {
		return fmt.Errorf("existing instance %q has no config in limactl list output", rec.Name)
	}
	if cfg.VMType != profile.VMType {
		return fmt.Errorf("vmType %q != %s", cfg.VMType, profile.VMType)
	}
	if cfg.Arch != profile.Arch {
		return fmt.Errorf("arch %q != %s", cfg.Arch, profile.Arch)
	}
	if cfg.SSH.ForwardAgent {
		return fmt.Errorf("ssh.forwardAgent is true; Torio forbids persistent agent forwarding")
	}
	if len(cfg.Mounts) != 0 {
		return fmt.Errorf("instance has %d mount(s); Torio requires mounts: []", len(cfg.Mounts))
	}
	if len(cfg.Images) != 1 {
		return fmt.Errorf("instance has %d image(s); Torio requires exactly one pinned image", len(cfg.Images))
	}
	img := cfg.Images[0]
	if img.Digest != profile.ImageDigest {
		return fmt.Errorf("image digest %q != promoted %q", img.Digest, profile.ImageDigest)
	}
	if img.Location != profile.ImageURL {
		return fmt.Errorf("image location %q != promoted %q", img.Location, profile.ImageURL)
	}
	return nil
}
