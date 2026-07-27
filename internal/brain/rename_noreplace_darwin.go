//go:build darwin

package brain

import (
	"runtime"
	"syscall"
	"unsafe"
)

const (
	sysRenameatxNP = 488
	renameExcl     = 0x00000004
)

// renameNoReplace uses Darwin's exclusive rename primitive. The kernel, not a
// check-then-act sequence, guarantees that an independently created export
// destination is never replaced.
func renameNoReplace(source, destination string) error {
	sourcePtr, err := syscall.BytePtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := syscall.BytePtrFromString(destination)
	if err != nil {
		return err
	}
	atFDCWD := ^uintptr(1) // -2, as required by renameatx_np(2).
	_, _, errno := syscall.Syscall6(
		sysRenameatxNP,
		atFDCWD,
		uintptr(unsafe.Pointer(sourcePtr)),
		atFDCWD,
		uintptr(unsafe.Pointer(destinationPtr)),
		renameExcl,
		0,
	)
	runtime.KeepAlive(sourcePtr)
	runtime.KeepAlive(destinationPtr)
	if errno != 0 {
		return errno
	}
	return nil
}
