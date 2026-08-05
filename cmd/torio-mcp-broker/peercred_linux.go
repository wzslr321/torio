//go:build linux

package main

import (
	"fmt"
	"net"
	"syscall"
)

// peerUID reports the uid the kernel attributes to the process at the other end
// of conn.
//
// SO_PEERCRED is read from the kernel, not presented by the caller, which is why
// the resulting audit line is evidence and not a note: ADR-0004 puts identity in
// the kernel precisely so that no secret can stand in for it.
//
// # What this does not buy
//
// It names a uid, not a program. Everything running as hermes looks identical
// here — the MCP client, a shell, a one-liner the agent wrote — so no rule may
// ever be built on "which caller this is" (ADR-0004 §3). The uid is recorded
// because a decision has to be attributable, and it is used for nothing else.
func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("peer credentials: %w", err)
	}
	var (
		ucred   *syscall.Ucred
		sockopt error
	)
	// Control runs the callback with the fd guaranteed not to be closed under it.
	// Reaching for conn.File() instead would dup the descriptor and put the
	// connection into blocking mode, which is a lasting change to how the whole
	// session behaves for one syscall's sake.
	if err := raw.Control(func(fd uintptr) {
		ucred, sockopt = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("peer credentials: %w", err)
	}
	if sockopt != nil {
		return 0, fmt.Errorf("peer credentials: %w", sockopt)
	}
	return ucred.Uid, nil
}
