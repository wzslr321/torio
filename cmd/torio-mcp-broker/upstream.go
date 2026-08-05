package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// upstream is the broker's link to one service's MCP server: one message out,
// one reply back.
//
// It is an interface for two reasons. The first is testability — every
// enforcement rule in this binary can be exercised against a fake without a
// network. The second is ADR-0004 §8: the endpoint is configurable so a later
// egress decision can put a proxy in front of it, and an interface is where that
// substitution costs nothing.
//
// Two rules bind every implementation:
//
//   - request is valid only for the duration of the call. It holds tool call
//     arguments, so an implementation that retained it would create a second
//     copy of upstream content with a lifetime nobody manages (ADR-0004 §5).
//   - a returned error must not embed any part of a reply body. Errors are
//     logged; reply bodies must never be. An HTTP transport must therefore
//     report status and cause, never the response text that explained them.
//
// A nil reply means the message was a notification and there is nothing to send
// back. It is not an error.
type upstream interface {
	roundTrip(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
}

// pendingUpstream is the seam where the HTTP transport will go, and until it
// does it is the honest thing to have here: a broker that enforces policy and
// carries nothing.
//
// It fails rather than pretends. A stub that answered with an empty result would
// make an unfinished broker look like a working one, and the whole point of
// ADR-0004 is that what the broker will and will not do is legible.
type pendingUpstream struct {
	// endpoint is this service's upstream_endpoint, taken from its policy
	// document. It is held rather than ignored so the error names where traffic
	// was meant to go, and so nothing in this binary has to invent a URL.
	endpoint string
}

func (u pendingUpstream) roundTrip(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, fmt.Errorf("no transport to %s: this broker enforces policy but does not yet carry traffic upstream", u.endpoint)
}
