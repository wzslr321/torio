//go:build !darwin && !linux

package transfer

import (
	"io/fs"
	"os"
)

func openRegularNoFollow(string) (*os.File, fs.FileInfo, error) {
	return nil, nil, privateError("secure source open is unavailable on this host")
}
