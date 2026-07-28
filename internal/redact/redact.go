// Package redact provides central redaction of sensitive material so that
// diagnostics, logs, and error messages never carry secrets (AGENTS §6,
// threat TM-12). Secrets are always rendered as the fixed Placeholder.
//
// Redaction is by shape only. A companion type that also masked literal secret
// values registered at runtime existed here and was never constructed by
// production code: Torio ingests no runtime secret to register — config is
// non-secret by contract, no credentials are stored, and the operator's push
// capability travels through a forwarded SSH agent Torio never sees. It was
// removed rather than kept as an unexercised path; reinstating it is mechanical
// if Torio ever does take a secret in.
package redact

import (
	"regexp"
)

// Placeholder is the fixed replacement for any redacted material.
const Placeholder = "[REDACTED]"

// patterns match well-known secret shapes. They mirror the shapes rejected by
// scripts/validate_artifacts.py and are matched anywhere within a string.
var patterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),                             // OpenAI-like API key
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),                        // GitHub token
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),                      // Slack token
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                  // AWS access key ID
	regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`), // private key header
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{10,}`),              // bearer token
}

// String returns s with any material matching a known secret shape replaced by
// Placeholder.
func String(s string) string {
	for _, p := range patterns {
		s = p.ReplaceAllString(s, Placeholder)
	}
	return s
}

// Slice returns a redacted copy of in without mutating it.
func Slice(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = String(v)
	}
	return out
}
