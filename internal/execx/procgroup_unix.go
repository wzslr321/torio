//go:build unix

package execx

import (
	"os/exec"
	"syscall"
)

// processGroupSupported reports that this platform can clean up the spawned
// process tree on cancellation. On unix we place the child in its own process
// group and signal the whole group.
const processGroupSupported = true

// setProcessGroup makes the child a process-group leader so its descendants
// share its group id and can be signalled together.
func setProcessGroup(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// killProcessGroup force-kills the child's whole process group. It is a no-op
// if the process has not started.
func killProcessGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	// Negative pid targets the process group led by the child.
	return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
}
