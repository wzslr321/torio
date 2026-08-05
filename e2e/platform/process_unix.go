//go:build darwin || linux

package platform

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

const (
	processTerminationGrace = time.Second
	processWaitDelay        = 2 * time.Second
)

func configureProcessCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = processWaitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid := cmd.Process.Pid
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		deadline := time.Now().Add(processTerminationGrace)
		for processGroupExists(pgid) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if processGroupExists(pgid) {
			if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				return err
			}
		}
		return nil
	}
}

func processGroupExists(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
