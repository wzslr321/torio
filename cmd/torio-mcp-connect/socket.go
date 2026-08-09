package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path/filepath"
	"syscall"

	"github.com/wzslr321/torio/internal/lima"
)

const socketDir = "/run/torio-mcp"

func socketPath(base, service string) (string, error) {
	if err := lima.ValidateServiceName(service); err != nil {
		return "", err
	}
	return filepath.Join(base, service+".sock"), nil
}

type dialError struct {
	exit int
	msg  string
}

func (e *dialError) Error() string { return e.msg }

func dial(path string) (*net.UnixConn, error) {
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err == nil {
		return conn, nil
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, &dialError{exit: exitNoBroker, msg: fmt.Sprintf("no broker socket at %s; run `torio mcp install` and `torio mcp login` on the host", path)}
	case errors.Is(err, fs.ErrPermission):
		return nil, &dialError{exit: exitDenied, msg: fmt.Sprintf("permission denied opening %s; `torio mcp status` verifies the torio-mcp-clients boundary", path)}
	case errors.Is(err, syscall.ECONNREFUSED):
		return nil, &dialError{exit: exitBrokerDown, msg: fmt.Sprintf("nothing is listening at %s; inspect torio-mcp-broker.service", path)}
	default:
		return nil, &dialError{exit: exitInternal, msg: fmt.Sprintf("cannot connect to %s", path)}
	}
}
