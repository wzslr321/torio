//go:build !linux

package main

import (
	"errors"
	"net"
)

func peerUID(*net.UnixConn) (uint32, error) {
	return 0, errors.New("SO_PEERCRED is available only in the Linux guest build")
}
