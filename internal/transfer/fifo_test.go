package transfer

import "syscall"

// makeFIFO creates a named pipe so the filter can be proven against a real
// special file rather than a mocked mode bit.
func makeFIFO(path string) error {
	return syscall.Mkfifo(path, 0o600)
}
