//go:build linux

package brain

import (
	"runtime"
	"syscall"
	"unsafe"
)

const renameNoReplaceFlag = 1

func renameNoReplace(source, destination string) error {
	sourcePtr, err := syscall.BytePtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := syscall.BytePtrFromString(destination)
	if err != nil {
		return err
	}
	atFDCWD := ^uintptr(99) // -100, as required by renameat2(2).
	_, _, errno := syscall.Syscall6(
		syscall.SYS_RENAMEAT2,
		atFDCWD,
		uintptr(unsafe.Pointer(sourcePtr)),
		atFDCWD,
		uintptr(unsafe.Pointer(destinationPtr)),
		renameNoReplaceFlag,
		0,
	)
	runtime.KeepAlive(sourcePtr)
	runtime.KeepAlive(destinationPtr)
	if errno != 0 {
		return errno
	}
	return nil
}
