//go:build !unix

package transfer

import "io/fs"

// hardLinked cannot be answered without a Unix stat, so every entry fails
// closed on platforms Torio does not target.
func hardLinked(fs.FileInfo) bool { return true }
