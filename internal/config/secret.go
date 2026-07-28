package config

import (
	"errors"

	"github.com/wzslr321/torio/internal/redact"
)

// containsSecretShape reports whether s carries material matching a well-known
// secret shape. Config is non-secret by contract (AGENTS §6), so any such match
// is rejected. It reuses the central redactor as the single source of truth for
// secret shapes: if redaction would change the string, a secret shape is
// present.
func containsSecretShape(s string) bool {
	return redact.String(s) != s
}

// redactErr guarantees no error leaving the config package carries secret-shaped
// material. The raw-byte pre-scan in parseFile cannot see a
// JSON-escaped secret (for example "ghp_…" has no literal "ghp_" on disk),
// so the decoder can turn it back into a secret that then reaches an error via
// %q interpolation of a decoded value or via the strict decoder echoing a
// decoded unknown field name. Redacting the final error text closes that gap
// regardless of how the secret entered, using the same central matcher as the
// detector so the boundary and the check stay consistent.
//
// A nil error stays nil. When no secret shape is present the original error is
// returned unchanged, preserving its wrapping chain and meaningful non-secret
// diagnostics; only a leaking message is flattened into a redacted one.
func redactErr(err error) error {
	if err == nil {
		return nil
	}
	if s := redact.String(err.Error()); s != err.Error() {
		return errors.New(s)
	}
	return err
}
