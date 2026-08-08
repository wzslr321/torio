package claudecode

import "github.com/wzslr321/torio/internal/backend"

// Status is nil until this backend's probe lands: the contract carries the
// capability before either backend declares one, so a poll that meets an
// undeclared probe reports exactly that rather than meeting a half-built one.
func (claudeBackend) Status() *backend.StatusSpec { return nil }
