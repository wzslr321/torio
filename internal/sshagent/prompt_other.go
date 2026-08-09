//go:build !darwin

package sshagent

import (
	"context"
	"time"

	"github.com/wzslr321/torio/internal/execx"
)

// askOperator refuses on every host that is not Darwin.
//
// Torio V1 supports Darwin as host (ADR-0002 §4), and this is the one place
// where an unsupported host must not degrade quietly: a proxy that signed
// without asking, because there was nothing to ask with, would be a weaker agent
// than `ssh -A` while claiming to be a stronger one. Linux keeps the package
// building and its tests running, with their own confirmer.
func askOperator(context.Context, execx.Runner, time.Duration, string) error {
	return errDenied
}
