//go:build !linux

package main

import (
	"errors"
	"net"
	"runtime"
)

// peerUID fails on every platform that is not the one the broker runs on.
//
// The broker is a Linux guest service; this file exists so the package still
// builds on a maintainer's macOS host, and it fails closed rather than
// substituting a plausible uid. A broker that invented the identity in its own
// audit line would produce records that look exactly like real ones — which is
// worse than not running at all, and it would only be discovered by someone
// trusting the log.
//
// Tests inject their own peer credential source, so this does not stand in the
// way of exercising the rules off Linux; it stands in the way of shipping there.
func peerUID(*net.UnixConn) (uint32, error) {
	return 0, errors.New("peer credentials are only readable on linux; the MCP broker is a guest service and does not serve on " + runtime.GOOS)
}
