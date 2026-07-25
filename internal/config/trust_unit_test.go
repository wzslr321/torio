package config

import (
	"io/fs"
	"strings"
	"testing"
)

// trust_unit_test.go — deterministic, platform-neutral coverage of the pure
// trust policy (verifyTrusted). Ownership mismatch is exercised here directly by
// passing uid != euid, so the rule is proven without root or a runtime bypass
// knob (ADR-0013 constraint 2). Integration coverage is in trust_test.go.

const euid uint32 = 1000

func TestVerifyTrustedAcceptsPrivateOwnedCorrectType(t *testing.T) {
	if err := verifyTrusted("/p", objRegular, objRegular, 0o600, euid, euid); err != nil {
		t.Errorf("private, owned, regular file must be accepted: %v", err)
	}
	if err := verifyTrusted("/d", objDir, objDir, 0o700, euid, euid); err != nil {
		t.Errorf("private, owned directory must be accepted: %v", err)
	}
}

func TestVerifyTrustedRejectsTypeMismatch(t *testing.T) {
	// Want a regular file, got a directory.
	if err := verifyTrusted("/p", objRegular, objDir, 0o600, euid, euid); err == nil {
		t.Errorf("directory where a regular file is wanted must be rejected")
	}
	// Want a directory, got a regular file.
	if err := verifyTrusted("/d", objDir, objRegular, 0o700, euid, euid); err == nil {
		t.Errorf("regular file where a directory is wanted must be rejected")
	}
	// A non-regular, non-directory object (fifo/device/socket) is rejected.
	if err := verifyTrusted("/o", objRegular, objOther, 0o600, euid, euid); err == nil {
		t.Errorf("non-regular object must be rejected")
	}
}

func TestVerifyTrustedRejectsModePermissive(t *testing.T) {
	for _, perm := range []fs.FileMode{0o640, 0o604, 0o644, 0o660, 0o777, 0o601, 0o610} {
		if err := verifyTrusted("/p", objRegular, objRegular, perm, euid, euid); err == nil {
			t.Errorf("perm %#o has group/other access and must be rejected", perm)
		}
	}
}

func TestVerifyTrustedRejectsForeignOwner(t *testing.T) {
	err := verifyTrusted("/p", objRegular, objRegular, 0o600, 1234, euid)
	if err == nil {
		t.Fatalf("object owned by a foreign uid must be rejected")
	}
	// Diagnostic mentions ownership, and carries no secret material.
	if !strings.Contains(err.Error(), "not owned by the effective user") {
		t.Errorf("rejection reason is not ownership: %q", err.Error())
	}
}

// TestVerifyTrustedRootExpectsRootOwned documents the root case: when the
// effective user is root (euid 0), only a root-owned object matches; a file
// owned by a normal user is rejected fail-closed.
func TestVerifyTrustedRootExpectsRootOwned(t *testing.T) {
	if err := verifyTrusted("/p", objRegular, objRegular, 0o600, 0, 0); err != nil {
		t.Errorf("as root, a root-owned private regular file must be accepted: %v", err)
	}
	if err := verifyTrusted("/p", objRegular, objRegular, 0o600, 501, 0); err == nil {
		t.Errorf("as root, a user-owned file must be rejected (strict uid==euid)")
	}
}
