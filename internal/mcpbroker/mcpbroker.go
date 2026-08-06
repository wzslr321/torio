// Package mcpbroker holds the policy engine of the MCP credential broker
// (ADR-0004): the grant a service is given, the decision taken on one tool call,
// and the audit record that decision leaves behind.
//
// The package deliberately knows nothing about credentials, sockets or
// upstreams. Custody lives with the broker's own guest identity, transport lives
// with the unix socket whose peer the kernel identifies; what is left is the
// question this package answers — may this caller invoke this tool on this
// service — and it is answered from a document the agent can read and cannot
// write.
package mcpbroker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Set is the effective policy of the whole broker: every service it will speak
// for, and the tools granted on each.
//
// The zero value is a usable, empty policy that denies everything. That is not a
// convenience: a broker that failed to load its policy, or has not loaded one
// yet, must not be able to allow a call by accident.
type Set struct {
	// services is keyed by service name. It is unexported and never handed out —
	// Grants copies — so no caller can widen a grant through a returned value.
	services map[string]service
}

// service is one loaded policy document, reduced to what a decision needs.
type service struct {
	upstreamEndpoint string
	// tools maps an allowed tool name to its write classification. Absence from
	// this map is the denial: there is no pattern, prefix or wildcard form, so a
	// name that is not a key was never granted (ADR-0004 §4).
	tools map[string]bool
}

// Reason explains a Decision. It exists so the two ways a call can be denied
// stay distinguishable: they read alike to whoever was denied and mean opposite
// things to an operator. "No such service" is a broker that was never configured
// for this connection; "tool not granted" is a service that was configured and
// deliberately not given this tool.
type Reason uint8

const (
	// ReasonUnknownService denies a service the broker holds no policy for. It is
	// the zero value on purpose: an unset Decision, like an unloaded Set, denies.
	ReasonUnknownService Reason = iota
	// ReasonToolNotGranted denies a tool the service's policy does not list.
	ReasonToolNotGranted
	// ReasonGranted allows a tool the service's policy lists by name.
	ReasonGranted
)

func (r Reason) String() string {
	switch r {
	case ReasonUnknownService:
		return "unknown_service"
	case ReasonToolNotGranted:
		return "tool_not_granted"
	case ReasonGranted:
		return "granted"
	default:
		return "invalid"
	}
}

// Decision is the broker's verdict on one tool call.
//
// The zero value denies, which is what makes every "not found" path in Allow a
// single return rather than a rule that has to be remembered.
type Decision struct {
	// Allowed reports whether the call may proceed.
	Allowed bool
	// Reason explains the verdict.
	Reason Reason
}

// Allow reports whether service may invoke tool.
//
// Default deny, and matching is exact. There is no wildcard, prefix, suffix or
// case-insensitive form, and adding one would not be a feature — ADR-0004 §4
// grants tools by name precisely so that nobody has to reason about what a
// pattern covers. A name that is not in the document was not granted, however
// close it looks to one that was.
//
// Allow reads nothing from disk. The Set was validated when it was loaded, so a
// decision cannot be changed by anything that happens to the policy directory
// while the broker is serving; a reload is an explicit act.
func (s Set) Allow(service, tool string) Decision {
	svc, ok := s.services[service]
	if !ok {
		return Decision{Reason: ReasonUnknownService}
	}
	if _, granted := svc.tools[tool]; !granted {
		return Decision{Reason: ReasonToolNotGranted}
	}
	return Decision{Allowed: true, Reason: ReasonGranted}
}

// Grant is the complete effective grant of a Set, in a form a caller can render
// without reaching back into the broker. It exists because ADR-0004 makes
// legibility the point of the whole arrangement: the answer to "what is granted"
// must be enumerable and machine-readable, not inferred from an installer's
// history.
type Grant struct {
	// Services is every service the broker speaks for, ordered by name so two
	// reports of the same policy are byte-identical.
	Services []ServiceGrant
}

// ServiceGrant is the grant held by one service.
type ServiceGrant struct {
	// Name is the service name, which is also its policy document's file stem.
	Name string
	// UpstreamEndpoint is where the broker sends this service's traffic. It is
	// reported, not just held: an operator asking what is granted is also asking
	// where the data goes (ADR-0004 §8).
	UpstreamEndpoint string
	// Tools is every allowed tool, ordered by name.
	Tools []ToolGrant
	// WriteTools is how many of Tools are write-classified. ADR-0004 requires a
	// report to be able to state the number of granted write tools, and a count
	// derived at report time cannot drift from the list it summarises.
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
// value it owns, so nothing it does to that value can reach the policy the
// broker is deciding against.
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
// formatting and tool order. A running broker publishes this value so the
// control plane can prove that the process enforcing policy loaded the same
// grant that status just parsed from disk.
func (s Set) Digest() string {
	body, err := json.Marshal(s.Grants())
	if err != nil {
		// Grant contains only strings, booleans, integers and slices. A marshal
		// failure would mean a programmer changed that invariant without changing
		// this method; it is not an operator-controlled policy error.
		panic("marshal effective MCP policy: " + err.Error())
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
