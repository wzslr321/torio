//go:build linux

package main

import (
	"errors"
	"net"
	"syscall"
)

func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, errors.New("get peer socket")
	}
	var uid uint32
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			sockErr = err
			return
		}
		uid = cred.Uid
	}); err != nil {
		return 0, errors.New("inspect peer socket")
	}
	if sockErr != nil {
		return 0, errors.New("read peer credentials")
	}
	return uid, nil
}
