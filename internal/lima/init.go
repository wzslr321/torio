package lima

import (
	"context"
	"fmt"
	"os"
)

// Init creates the Torio VM from the embedded, Gate-0-pinned template, or
// succeeds idempotently when an existing instance already matches the trusted
// security pins (image digest/URL, empty mounts, forwardAgent=false, vz/aarch64).
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
	out := InitResult{
		ImageLocation: PromotedImageURL,
		ImageDigest:   PromotedImageDigest,
	}

	opts = opts.withDefaults()
	if err := validateOperatorUser(opts.OperatorUser); err != nil {
		return out, &Error{Op: op, Kind: KindVerificationFailed, Err: err}
	}

	rec, err := a.currentInstance(ctx, op)
	if err != nil {
		return out, err
	}
	if rec != nil {
		if err := verifyCompatibleConfig(rec); err != nil {
			return out, &Error{Op: op, Kind: KindIncompatible, Err: err}
		}
		out.Created = false
		return out, nil
	}

	body, err := renderTemplate(opts)
	if err != nil {
		return out, &Error{Op: op, Kind: KindVerificationFailed, Err: err}
	}
	path, err := writePrivateTemplate(body)
	if err != nil {
		return out, &Error{Op: op, Kind: KindVerificationFailed, Err: fmt.Errorf("write template: %w", err)}
	}
	defer func() { _ = os.Remove(path) }()

	// Exact Gate 0 argv: --tty=false before the template path (FINDINGS.md).
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
	if err := verifyCompatibleConfig(rec); err != nil {
		return out, &Error{Op: op, Kind: KindPostconditionFailed, Err: err}
	}

	out.Created = true
	return out, nil
}

func verifyCompatibleConfig(rec *instanceRecord) error {
	cfg := rec.Config
	if cfg == nil {
		return fmt.Errorf("existing instance %q has no config in limactl list output", rec.Name)
	}
	if cfg.VMType != "vz" {
		return fmt.Errorf("vmType %q != vz", cfg.VMType)
	}
	if cfg.Arch != "aarch64" {
		return fmt.Errorf("arch %q != aarch64", cfg.Arch)
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
	if img.Digest != PromotedImageDigest {
		return fmt.Errorf("image digest %q != promoted %q", img.Digest, PromotedImageDigest)
	}
	if img.Location != PromotedImageURL {
		return fmt.Errorf("image location %q != promoted %q", img.Location, PromotedImageURL)
	}
	return nil
}
