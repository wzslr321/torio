//go:build darwin || linux

package transfer

import (
	"io/fs"
	"os"
	"syscall"
)

// openRegularNoFollow opens and validates the same descriptor, so a
// final-component symlink cannot be substituted between a path check and the
// content read. Torio V1 supports Darwin as host; Linux keeps unit tests and
// guest-shaped tooling on the same fail-closed primitive.
func openRegularNoFollow(path string) (*os.File, fs.FileInfo, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), "")
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, privateError("source entry is not a regular file")
	}
	return file, info, nil
}
