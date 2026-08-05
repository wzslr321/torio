package mcpbroker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// policyFileExt is the only extension the loader reads. Policy documents are
// JSON so they parse under the same fail-closed rules as the rest of Torio's
// on-disk state (internal/config): unknown fields rejected, one document per
// file, no silent normalisation.
const policyFileExt = ".json"

// The bounds below exist because the policy directory is read on broker start
// and on every drift check, and nothing about "a few services, each granted a
// listed set of tools" is open-ended. A bound that is generous but finite costs
// an operator nothing and denies a directory that is hostile — or merely
// corrupt — the ability to make the broker unbootable by making the read
// expensive.
const (
	// maxServices bounds how many services one broker speaks for.
	maxServices = 32
	// maxToolsPerService bounds one service's grant. The Atlassian server
	// migrated in ADR-0004 exposes 40 tools, so this leaves generous headroom.
	maxToolsPerService = 256
	// maxDocumentBytes bounds one policy document. A grant of maxToolsPerService
	// tools fits in a few tens of kilobytes.
	maxDocumentBytes = 256 << 10
)

// MaxServiceNameLen bounds a service name. It is short because the name is a
// slug an operator types and reads, not a description — and because the bound
// is load-bearing outside this package: cmd/torio-mcp-connect resolves
// /run/torio-mcp/<service>.sock, which must fit the kernel's ~104-byte sun_path
// limit. Exported so that arithmetic can be asserted rather than assumed.
const MaxServiceNameLen = 32

// maxUpstreamLen bounds the upstream endpoint URL.
const maxUpstreamLen = 512

// maxToolNameLen bounds a granted tool name. Upstream MCP servers name tools in
// tens of bytes; the bound exists so a policy document cannot smuggle a long
// string into every audit line the tool ever produces.
const maxToolNameLen = 128

// servicePattern is the accepted service name: a lowercase slug of ASCII
// letters, digits and inner hyphens.
//
// The charset is restrictive because the name leaves this package. ADR-0004
// derives the broker's socket path from it (/run/torio-mcp/<service>.sock) and
// the name is echoed in reports and audit lines. Nothing here can traverse a
// directory, be re-read as a flag, or move a terminal cursor.
var servicePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

// ValidateServiceName reports whether name may be used as a service, and is
// exported because it is the single point where that question is answered.
//
// Two packages depend on the same answer for opposite halves of one path: this
// one reads a policy document's filename stem, and cmd/torio-mcp-connect turns
// the operator's argv into /run/torio-mcp/<service>.sock. Written twice, the
// two copies would agree until somebody widened one of them, and the failure
// would not be a rejected name — it would be a socket the broker binds and
// nothing can reach, or the reverse. The bound also keeps the resolved address
// inside the kernel's sun_path limit (~104 bytes), which only holds while both
// sides agree on it.
func ValidateServiceName(name string) error {
	if name == "" {
		return errors.New("service name is required")
	}
	// Length first, so an oversized name is reported without being echoed into
	// a diagnostic the caller chose the contents of.
	if len(name) > MaxServiceNameLen {
		return fmt.Errorf("service name is longer than %d bytes", MaxServiceNameLen)
	}
	if !servicePattern.MatchString(name) {
		return fmt.Errorf("service name %q must be a lowercase slug of ASCII letters, digits and inner hyphens", name)
	}
	return nil
}

// toolPattern is the accepted tool name. It is wider than servicePattern
// because the names come from upstream servers rather than from Torio —
// Atlassian uses camelCase, Slack snake_case, namespaced servers dots — but it
// still admits no whitespace, no control byte and no character with meaning to a
// shell, a path or a log line.
//
// Note what it also excludes: `*`, `?` and every other glob character. Not
// because they would match anything — this package never pattern-matches — but
// so that a document written by someone who assumed they would is rejected
// instead of quietly granting a single, oddly named tool (ADR-0004 §4).
var toolPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$`)

var (
	// upstreamHostPattern is the accepted host of an upstream endpoint: a DNS
	// name or a literal IPv4 address. It is deliberately narrow — the broker
	// reaches a handful of known services and one local proxy, not the internet.
	upstreamHostPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,252}[A-Za-z0-9])?$`)
	// upstreamPortPattern is the accepted explicit port.
	upstreamPortPattern = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// PolicySchemaVersion is the only supported policy document version. A document
// declaring any other version is rejected rather than migrated: a grant this
// binary can only partly interpret must not be enforced as if it understood it,
// and the operator who wrote it is the one who decides what the new version
// means.
const PolicySchemaVersion = "1"

// Load reads every policy document in dir and returns the effective policy Set.
//
// Production reads /etc/torio-mcp/policy.d, whose documents are root-owned and
// world-readable on purpose: ADR-0004 requires the grant to be legible to
// everyone, the agent included, while the credentials it unlocks are not. The
// directory is a parameter so the rule can be exercised without root.
func Load(dir string) (Set, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Set{}, fmt.Errorf("read policy directory: %w", err)
	}

	documents := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() {
			// DirEntry.Type() carries lstat semantics, so a symlink is reported as
			// one rather than followed. That matters here and not only for tidiness:
			// the directory is root-owned, but a symlink's content is not, so a link
			// to a path under the agent's home would hand the grant to the identity
			// ADR-0004 exists to keep away from it.
			return Set{}, fmt.Errorf("policy %s: not a regular file; a policy document must live in the policy directory, not point at another path", name)
		}
		if !strings.HasSuffix(name, policyFileExt) {
			// Skipping the file would be worse than refusing it: a leftover
			// `<service>.yaml` or `<service>.json.disabled` is a document somebody
			// believes is in force, and a policy engine that silently ignores files
			// makes the directory unreadable as a statement of what is granted.
			return Set{}, fmt.Errorf("policy %s: every file in the policy directory must be a %s policy document", name, policyFileExt)
		}
		data, err := readBounded(filepath.Join(dir, name))
		if err != nil {
			// An unreadable document is never treated as one that grants nothing:
			// that would let whoever can break a read choose which policies apply.
			return Set{}, fmt.Errorf("read policy %s: %w", name, err)
		}
		documents[name] = data
	}
	return ParseDocuments(documents)
}

// ParseDocuments validates an in-memory policy directory using the exact same
// schema and bounds as Load. It exists for control planes that retrieve bounded
// documents from another trust domain and must not recreate the broker's parser.
func ParseDocuments(documents map[string][]byte) (Set, error) {
	if len(documents) > maxServices {
		return Set{}, fmt.Errorf("policy directory holds more than the maximum %d services", maxServices)
	}
	names := make([]string, 0, len(documents))
	for name := range documents {
		names = append(names, name)
	}
	sort.Strings(names)

	set := Set{services: make(map[string]service, len(documents))}
	for _, name := range names {
		if !strings.HasSuffix(name, policyFileExt) {
			return Set{}, fmt.Errorf("policy %s: every file in the policy directory must be a %s policy document", name, policyFileExt)
		}
		data := documents[name]
		if len(data) > maxDocumentBytes {
			return Set{}, fmt.Errorf("policy %s: document is larger than the maximum %d bytes", name, maxDocumentBytes)
		}
		stem := strings.TrimSuffix(name, policyFileExt)
		svc, err := parseDocument(data, stem)
		if err != nil {
			// One bad document fails the whole load. A half-applied policy set is a
			// grant nobody wrote.
			return Set{}, fmt.Errorf("policy %s: %w", name, err)
		}
		set.services[stem] = svc
	}
	return set, nil
}

// readBounded reads at most maxDocumentBytes+1 bytes from path and reports a
// document that exceeds the bound as an error.
//
// The read is bounded rather than the file stat'ed: a stat says how big the file
// was, and the read is what allocates. Reading one byte past the limit is what
// distinguishes "exactly at the bound" from "too large" without a second syscall
// that could disagree with the first.
func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDocumentBytes {
		return nil, fmt.Errorf("document is larger than the maximum %d bytes", maxDocumentBytes)
	}
	return data, nil
}

// documentJSON is the wire form of a policy document.
//
// Tools is a pointer so an absent key is distinguishable from an empty list.
// Both are legal JSON and Go would read them identically, but they are different
// statements: "this service is granted nothing" is a decision, and "I did not
// write the grant yet" is an unfinished document.
type documentJSON struct {
	SchemaVersion    string      `json:"schema_version"`
	Service          string      `json:"service"`
	UpstreamEndpoint string      `json:"upstream_endpoint"`
	Tools            *[]toolJSON `json:"tools"`
}

// toolJSON is the wire form of one granted tool.
//
// Writes is a pointer for the same reason, with more at stake: Go's zero value
// for a bool is false, so an omitted classification would silently declare a
// write tool read-only and shrink the granted-write count ADR-0004 requires the
// broker to be able to report.
type toolJSON struct {
	Name   string `json:"name"`
	Writes *bool  `json:"writes"`
}

// parseDocument decodes and validates one policy document. stem is the file name
// without its extension, which the document's own service field must match.
func parseDocument(data []byte, stem string) (service, error) {
	// Probe the declared version before the strict decode, so a document written
	// for another schema is refused by version rather than by whichever field
	// happens to be unknown to the one wire form this binary has.
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return service{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if probe.SchemaVersion != PolicySchemaVersion {
		return service{}, fmt.Errorf("schema_version %q is not supported (want %q)",
			probe.SchemaVersion, PolicySchemaVersion)
	}

	var raw documentJSON
	if err := decodeStrict(data, &raw); err != nil {
		return service{}, err
	}

	if err := ValidateServiceName(stem); err != nil {
		return service{}, err
	}
	if raw.Service != stem {
		return service{}, fmt.Errorf("declares service %q but is named for service %q; a policy document has one identity",
			raw.Service, stem)
	}

	if err := validateUpstreamEndpoint(raw.UpstreamEndpoint); err != nil {
		return service{}, fmt.Errorf("upstream_endpoint %w", err)
	}

	if raw.Tools == nil {
		return service{}, errors.New(`must declare "tools"; grant an empty list to connect a service without granting it anything`)
	}

	if len(*raw.Tools) > maxToolsPerService {
		return service{}, fmt.Errorf("grants %d tools, more than the maximum %d", len(*raw.Tools), maxToolsPerService)
	}

	svc := service{
		upstreamEndpoint: raw.UpstreamEndpoint,
		tools:            make(map[string]bool, len(*raw.Tools)),
	}
	for _, t := range *raw.Tools {
		if len(t.Name) > maxToolNameLen || !toolPattern.MatchString(t.Name) {
			// %q escapes any non-printable byte, so naming the offender cannot
			// itself move an operator's cursor.
			return service{}, fmt.Errorf("tool name %q must be letters, digits, and inner dots, hyphens or underscores, at most %d bytes",
				t.Name, maxToolNameLen)
		}
		if _, dup := svc.tools[t.Name]; dup {
			return service{}, fmt.Errorf("tool %q is granted twice", t.Name)
		}
		if t.Writes == nil {
			return service{}, fmt.Errorf(`tool %q must declare "writes"; a granted tool is classified by hand, never inferred from its name`, t.Name)
		}
		svc.tools[t.Name] = *t.Writes
	}
	return svc, nil
}

// validateUpstreamEndpoint enforces the rules of the endpoint the broker sends a
// service's traffic to. ADR-0004 §8 requires it to be configurable so a later
// egress decision can put a proxy in front of it without redesigning anything,
// which is also why no URL is hardcoded anywhere in this package.
//
// Configurable is not arbitrary. The document is world-readable by design, so
// the endpoint must not be a place a credential can hide: userinfo, query and
// fragment are the three components that carry one in practice, and all three
// are refused. http:// is accepted alongside https:// because the anticipated
// proxy is local, and demanding TLS to loopback would buy nothing while costing
// a certificate nobody maintains.
func validateUpstreamEndpoint(endpoint string) error {
	switch {
	case endpoint == "":
		return errors.New("is required; the broker must be told where a service's traffic goes")
	case len(endpoint) > maxUpstreamLen:
		return fmt.Errorf("is longer than %d bytes", maxUpstreamLen)
	case strings.ContainsFunc(endpoint, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return errors.New("contains control characters")
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		// The rejected value is not echoed here: url.Parse errors quote the input.
		return errors.New("is not a parsable URL")
	}
	switch u.Scheme {
	case "https", "http":
	default:
		return errors.New("must be an https:// or http:// URL")
	}
	if u.User != nil {
		return errors.New("must not embed credentials in the URL")
	}
	if u.ForceQuery || u.RawQuery != "" {
		return errors.New("must not carry a query")
	}
	if u.Fragment != "" {
		return errors.New("must not carry a fragment")
	}
	if !upstreamHostPattern.MatchString(u.Hostname()) {
		return errors.New("must name a host")
	}
	if port := u.Port(); port != "" && !upstreamPortPattern.MatchString(port) {
		return errors.New("has an unsupported port")
	}
	return nil
}

// decodeStrict decodes one JSON document from data into v, rejecting unknown
// fields at every level.
//
// A policy document is a grant, so a field the decoder cannot place is not a
// harmless extra: it is either a typo that silently disables the rule its author
// meant to write, or a schema this binary is too old to enforce. Both are
// refusals. Anything after the document is refused for the same reason: it is a
// grant that would never be enforced but would still be read by whoever audits
// the file.
//
// Decoder.More() only tests for a next element within the current array or
// object, so a stray closing delimiter can slip past it; a second Decode that
// must return io.EOF is the reliable end-of-input check, and it still yields EOF
// after trailing whitespace.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON or unknown field: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return errors.New("unexpected trailing data after JSON document")
	}
	return nil
}
