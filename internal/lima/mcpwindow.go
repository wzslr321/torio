package lima

import (
	"context"
	"fmt"
	"time"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

const mcpWindowOp = "mcp_allow_write"

// TorioMCPWindowDir is where the broker looks for open write windows. It sits
// inside the broker's 0700 home, so the identity the agent has a shell as can
// neither read nor create one — which is the only reason a window means
// anything.
var TorioMCPWindowDir = TorioMCPHome + "/" + mcpbroker.WriteWindowDirName

// WriteWindowReport is the outcome of opening a window.
type WriteWindowReport struct {
	Instance string
	Service  string
	Until    time.Time
}

// OpenWriteWindow grants write-classified MCP tools for one service until
// `until`, by placing the expiry in the broker's own home.
//
// This is the operator's half of the mechanism, and it is deliberately the same
// shape `torio project shell` gives Git: capability appears because a human
// asked for it, is bounded in time, and ends without anyone remembering to
// close it. Nothing the agent can do opens or extends a window — it has no sudo
// and cannot enter the broker's home.
//
// The expiry travels as stdin rather than as an argument, so it never appears
// in a process listing on the guest. The file is written *as* the broker
// identity rather than as root: the broker replaces its own windows, and a
// root-owned file inside a 0700 directory it owns is one it may later be unable
// to move out of the way.
func (a *Adapter) OpenWriteWindow(ctx context.Context, service string, until time.Time) (WriteWindowReport, error) {
	if err := mcpbroker.ValidateServiceName(service); err != nil {
		// Checked before anything reaches the guest: the name becomes a filename
		// there, and the one shared rule is what keeps it inside the directory.
		return WriteWindowReport{}, &Error{Op: mcpWindowOp, Kind: KindVerificationFailed, Err: err}
	}
	if !until.After(time.Now()) {
		return WriteWindowReport{}, &Error{Op: mcpWindowOp, Kind: KindVerificationFailed,
			Err: fmt.Errorf("write window would already be closed; pick a duration in the future")}
	}

	path := TorioMCPWindowDir + "/" + service

	if err := a.windowMutate(ctx, "sudo", "-n", "install", "-d",
		"-o", TorioMCPUser, "-g", TorioMCPUser, "-m", "0700", TorioMCPWindowDir); err != nil {
		return WriteWindowReport{}, err
	}

	res, err := a.SSHInput(ctx, []byte(mcpbroker.FormatWriteWindow(until)),
		[]string{"sudo", "-n", "-u", TorioMCPUser, "--", "tee", path})
	if err != nil {
		return WriteWindowReport{}, err
	}
	if res.ExitCode != 0 {
		return WriteWindowReport{}, &Error{Op: mcpWindowOp, Kind: KindCommandFailed,
			Err: fmt.Errorf("could not write the window file (exit %d)", res.ExitCode)}
	}

	// tee creates 0644. The window is not a secret, but nothing in the broker's
	// home should be readable outside it, and a mode nobody set is a mode nobody
	// checked.
	if err := a.windowMutate(ctx, "sudo", "-n", "chmod", "600", path); err != nil {
		return WriteWindowReport{}, err
	}

	return WriteWindowReport{Instance: InstanceName, Service: service, Until: until}, nil
}

func (a *Adapter) windowMutate(ctx context.Context, argv ...string) error {
	res, err := a.SSH(ctx, argv)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return &Error{Op: mcpWindowOp, Kind: KindCommandFailed,
			Err: fmt.Errorf("guest command exited %d", res.ExitCode)}
	}
	return nil
}
