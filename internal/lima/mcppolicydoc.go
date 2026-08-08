package lima

// This file is the MCP broker policy engine: parsing and validating a policy
// document set, and reducing it to the grant a report renders (ADR-0004 §4).
//
// It was extracted from the standalone `internal/mcpbroker` package when the
// unfinished broker/relay daemon was removed (ADR-0008). The daemon never
// shipped, but this parser did: verifyPolicyDocuments in mcppolicy.go calls
// ParseDocuments to prove the guest's root-owned policy directory holds a
// valid grant, and that is part of the release `torio mcp status`/`mcp
// install` surface. internal/lima is now the sole owner. A future broker
// implementation, if one is built, is expected to import this logic from here
// rather than reintroduce a separate copy.
//
// The package deliberately knows nothing about credentials, sockets or
// upstreams. Custody lives with the broker identity's guest account, transport
// lives with the unix socket whose peer the kernel identifies; what is left is
// the question this file answers — what does the policy grant — and it is
// answered from a document the agent can read and cannot write.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// policyFileExt is the only extension the parser reads. Policy documents are
// JSON so they parse under the same fail-closed rules as the rest of Torio's
// on-disk state (internal/config): unknown fields rejected, one document per
// file, no silent normalisation.
const policyFileExt = ".json"

// The bounds below exist because the policy directory is read on every drift
// check, and nothing about "a few services, each granted a listed set of
// tools" is open-ended. A bound that is generous but finite costs an operator
// nothing and denies a directory that is hostile — or merely corrupt — the
// ability to make verification expensive.
const (
	// maxServices bounds how many services one policy set speaks for.
	maxServices = 32
	// maxToolsPerService bounds one service's grant. The Atlassian server
	// migrated in ADR-0004 exposes 40 tools, so this leaves generous headroom.
	maxToolsPerService = 256
	// maxDocumentBytes bounds one policy document. A grant of
	// maxToolsPerService tools fits in a few tens of kilobytes.
	maxDocumentBytes = 256 << 10
)

// MaxPolicyServices and MaxPolicyDocumentBytes export the parser's allocation
// bounds to the delivered broker's bounded directory reader. The parser remains
// the sole owner of their values.
const (
	MaxPolicyServices      = maxServices
	MaxPolicyDocumentBytes = maxDocumentBytes
)

// MaxServiceNameLen bounds a service name. It is short because the name is a
// slug an operator types and reads, not a description. Exported so that
// arithmetic can be asserted rather than assumed.
const MaxServiceNameLen = 32

// maxUpstreamLen bounds the upstream endpoint URL.
const maxUpstreamLen = 512

// maxToolNameLen bounds a granted tool name. Upstream MCP servers name tools
// in tens of bytes; the bound exists so a policy document cannot smuggle a
// long string into every audit line the tool ever produces.
const maxToolNameLen = 128

// servicePattern is the accepted service name: a lowercase slug of ASCII
// letters, digits and inner hyphens.
//
// The charset is restrictive because the name is echoed in reports and (were
// the daemon to exist) would resolve a socket path. Nothing here can traverse
// a directory, be re-read as a flag, or move a terminal cursor.
var servicePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

// ValidateServiceName reports whether name may be used as a service.
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
// still admits no whitespace, no control byte and no character with meaning to
// a shell, a path or a log line.
//
// Note what it also excludes: `*`, `?` and every other glob character. Not
// because they would match anything — nothing here pattern-matches — but so
// that a document written by someone who assumed they would is rejected
// instead of quietly granting a single, oddly named tool (ADR-0004 §4).
var toolPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$`)

var (
	// upstreamHostPattern is the accepted host of an upstream endpoint: a DNS
	// name or a literal IPv4 address. It is deliberately narrow — a handful of
	// known services and one local proxy, not the internet.
	upstreamHostPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,252}[A-Za-z0-9])?$`)
	// upstreamPortPattern is the accepted explicit port.
	upstreamPortPattern = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// PolicySchemaVersion is the only supported policy document version. A
// document declaring any other version is rejected rather than migrated: a
// grant this binary can only partly interpret must not be enforced as if it
// understood it, and the operator who wrote it is the one who decides what the
// new version means.
const PolicySchemaVersion = "1"

// Set is the effective policy of a whole policy directory: every service it
// speaks for, and the tools granted on each.
//
// The zero value is a usable, empty policy that denies everything. That is not
// a convenience: verification that failed to load a policy, or has not loaded
// one yet, must not be able to report a grant by accident.
type Set struct {
	// services is keyed by service name. It is unexported and never handed
	// out — Grants copies — so no caller can widen a grant through a returned
	// value.
	services map[string]service
}

// service is one loaded policy document, reduced to what a grant needs.
type service struct {
	upstreamEndpoint string
	// tools maps an allowed tool name to its write classification. Absence
	// from this map is the denial: there is no pattern, prefix or wildcard
	// form, so a name that is not a key was never granted (ADR-0004 §4).
	tools map[string]bool
}

// Grant is the complete effective grant of a Set, in a form a caller can
// render without reaching back into the policy. It exists because ADR-0004
// makes legibility the point of the whole arrangement: the answer to "what is
// granted" must be enumerable and machine-readable, not inferred from an
// installer's history.
type Grant struct {
	// Services is every service the policy speaks for, ordered by name so two
	// reports of the same policy are byte-identical.
	Services []ServiceGrant
}

// ServiceGrant is the grant held by one service.
type ServiceGrant struct {
	// Name is the service name, which is also its policy document's file
	// stem.
	Name string
	// UpstreamEndpoint is where this service's traffic is destined. It is
	// reported, not just held: an operator asking what is granted is also
	// asking where the data goes (ADR-0004 §8).
	UpstreamEndpoint string
	// Tools is every allowed tool, ordered by name.
	Tools []ToolGrant
	// WriteTools is how many of Tools are write-classified. ADR-0004 requires
	// a report to be able to state the number of granted write tools, and a
	// count derived at report time cannot drift from the list it summarises.
	WriteTools int
}

// ToolGrant is one allowed tool and its write classification.
type ToolGrant struct {
	// Name is the exact tool name. Grants are enumerated by name; nothing is
	// matched by pattern.
	Name string
	// Writes reports whether invoking the tool mutates upstream state, as
	// declared by the policy document. It is never inferred from the name.
	Writes bool
}

// Grants returns the complete effective grant of s.
//
// Everything returned is freshly allocated. A caller renders a report from a
// value it owns, so nothing it does to that value can reach the policy being
// reported on.
func (s Set) Grants() Grant {
	g := Grant{Services: make([]ServiceGrant, 0, len(s.services))}
	for name, svc := range s.services {
		sg := ServiceGrant{
			Name:             name,
			UpstreamEndpoint: svc.upstreamEndpoint,
			Tools:            make([]ToolGrant, 0, len(svc.tools)),
		}
		for tool, writes := range svc.tools {
			sg.Tools = append(sg.Tools, ToolGrant{Name: tool, Writes: writes})
			if writes {
				sg.WriteTools++
			}
		}
		sort.Slice(sg.Tools, func(i, j int) bool { return sg.Tools[i].Name < sg.Tools[j].Name })
		g.Services = append(g.Services, sg)
	}
	sort.Slice(g.Services, func(i, j int) bool { return g.Services[i].Name < g.Services[j].Name })
	return g
}

// Digest identifies the complete effective grant, independent of policy-file
// formatting and tool order. It is the value verifyBrokerSockets compares
// against a running broker's own published digest, so a report and the
// process enforcing policy can be shown to be describing one grant rather than
// two.
func (s Set) Digest() string {
	body, err := json.Marshal(s.Grants())
	if err != nil {
		// Grant contains only strings, booleans, integers and slices. A
		// marshal failure would mean a programmer changed that invariant
		// without changing this method; it is not an operator-controlled
		// policy error.
		panic("marshal effective MCP policy: " + err.Error())
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// ParseDocuments validates an in-memory policy directory using the exact same
// schema and bounds a broker would apply reading it from disk. It exists for
// verification that retrieves bounded documents from the guest and must not
// recreate the parser by hand.
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
			// One bad document fails the whole load. A half-applied policy
			// set is a grant nobody wrote.
			return Set{}, fmt.Errorf("policy %s: %w", name, err)
		}
		set.services[stem] = svc
	}
	return set, nil
}

// documentJSON is the wire form of a policy document.
//
// Tools is a pointer so an absent key is distinguishable from an empty list.
// Both are legal JSON and Go would read them identically, but they are
// different statements: "this service is granted nothing" is a decision, and
// "I did not write the grant yet" is an unfinished document.
type documentJSON struct {
	SchemaVersion    string      `json:"schema_version"`
	Service          string      `json:"service"`
	UpstreamEndpoint string      `json:"upstream_endpoint"`
	Tools            *[]toolJSON `json:"tools"`
}

// toolJSON is the wire form of one granted tool.
//
// Writes is a pointer for the same reason, with more at stake: Go's zero
// value for a bool is false, so an omitted classification would silently
// declare a write tool read-only and shrink the granted-write count ADR-0004
// requires a report to be able to state.
type toolJSON struct {
	Name   string `json:"name"`
	Writes *bool  `json:"writes"`
}

// parseDocument decodes and validates one policy document. stem is the file
// name without its extension, which the document's own service field must
// match.
func parseDocument(data []byte, stem string) (service, error) {
	// Probe the declared version before the strict decode, so a document
	// written for another schema is refused by version rather than by
	// whichever field happens to be unknown to the one wire form this binary
	// has.
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
			// %q escapes any non-printable byte, so naming the offender
			// cannot itself move an operator's cursor.
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

// validateUpstreamEndpoint enforces the rules of the endpoint a service's
// traffic is sent to. ADR-0004 §8 requires it to be configurable so a later
// egress decision can put a proxy in front of it without redesigning
// anything, which is also why no URL is hardcoded anywhere in this file.
//
// Configurable is not arbitrary. The document is world-readable by design, so
// the endpoint must not be a place a credential can hide: userinfo, query and
// fragment are the three components that carry one in practice, and all three
// are refused. http:// is accepted alongside https:// because the anticipated
// proxy is local, and demanding TLS to loopback would buy nothing while
// costing a certificate nobody maintains.
func validateUpstreamEndpoint(endpoint string) error {
	switch {
	case endpoint == "":
		return errors.New("is required; the policy must state where a service's traffic goes")
	case len(endpoint) > maxUpstreamLen:
		return fmt.Errorf("is longer than %d bytes", maxUpstreamLen)
	case strings.ContainsFunc(endpoint, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return errors.New("contains control characters")
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		// The rejected value is not echoed here: url.Parse errors quote the
		// input.
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
// harmless extra: it is either a typo that silently disables the rule its
// author meant to write, or a schema this binary is too old to enforce. Both
// are refusals. Anything after the document is refused for the same reason: it
// is a grant that would never be enforced but would still be read by whoever
// audits the file.
//
// Decoder.More() only tests for a next element within the current array or
// object, so a stray closing delimiter can slip past it; a second Decode that
// must return io.EOF is the reliable end-of-input check, and it still yields
// EOF after trailing whitespace.
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
