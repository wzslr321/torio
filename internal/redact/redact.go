// Package redact provides central redaction of sensitive material so that
// diagnostics, logs, and error messages never carry secrets (AGENTS §6,
// threat TM-12). Secrets are always rendered as the fixed Placeholder.
package redact

import (
	"regexp"
	"sort"
	"strings"
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

// Redactor redacts known secret shapes and, additionally, any registered
// literal secret values (for example a token whose value is known at runtime
// but does not match a generic pattern).
type Redactor struct {
	// literals matches any registered literal in a single pass. It is nil when
	// no literals were registered.
	literals *regexp.Regexp
}

// New returns a Redactor that also masks each non-empty literal secret.
//
// Registered literals are matched longest-first in a single pass, so a shorter
// literal that is a prefix/substring of a longer one cannot leave a residue of
// the longer secret (e.g. New("abc", "abcdef") on "abcdef" yields the
// placeholder, never "def"). Order of registration does not matter.
func New(literals ...string) *Redactor {
	kept := make([]string, 0, len(literals))
	for _, l := range literals {
		if l != "" {
			kept = append(kept, l)
		}
	}
	if len(kept) == 0 {
		return &Redactor{}
	}
	// Go's regexp (RE2, leftmost-first alternation) prefers the earliest listed
	// alternative at a match position, so ordering longest-first makes the
	// longest containing literal win.
	sort.SliceStable(kept, func(i, j int) bool { return len(kept[i]) > len(kept[j]) })
	quoted := make([]string, len(kept))
	for i, l := range kept {
		quoted[i] = regexp.QuoteMeta(l)
	}
	return &Redactor{literals: regexp.MustCompile(strings.Join(quoted, "|"))}
}

// String redacts registered literals (single pass) and known secret shapes.
func (r *Redactor) String(s string) string {
	if r.literals != nil {
		s = r.literals.ReplaceAllString(s, Placeholder)
	}
	return String(s)
}

// Slice returns a redacted copy of in without mutating it.
func (r *Redactor) Slice(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = r.String(v)
	}
	return out
}
