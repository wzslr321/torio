//go:build !unix

package execx

import "os/exec"

// processGroupSupported reports that this platform does NOT clean up the
// spawned process tree on cancellation. On such platforms only the direct
// child is killed (os/exec default) and descendants may leak; callers relying
// on tree cleanup must run on a unix host. This boundary is explicit and
// tested only where processGroupSupported is true.
const processGroupSupported = false

// setProcessGroup is a no-op on non-unix platforms.
func setProcessGroup(c *exec.Cmd) {}

// killProcessGroup falls back to os/exec's default (direct child only).
func killProcessGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return c.Process.Kill()
}
