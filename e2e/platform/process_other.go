//go:build !darwin && !linux

package platform

import (
	"os/exec"
	"time"
)

func configureProcessCancellation(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}
