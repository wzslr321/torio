//go:build unix

package transfer

import (
	"io/fs"
	"syscall"
)

// hardLinked reports whether the file has more than one directory entry. A
// second link means the same bytes are reachable under another name, possibly
// outside the source root.
func hardLinked(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Link count is unreadable, so the property cannot be proven. Treat the
		// entry as hardlinked and skip it rather than staging it on faith.
		return true
	}
	return st.Nlink > 1
}
