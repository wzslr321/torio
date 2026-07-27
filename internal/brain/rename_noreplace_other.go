//go:build !darwin && !(linux && (amd64 || arm64))

package brain

import "errors"

func renameNoReplace(string, string) error {
	return errors.New("exclusive directory rename is unavailable on this host")
}
