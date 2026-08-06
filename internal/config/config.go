// Package config holds the typed runtime settings that bound a torio
// invocation.
package config

import (
	"fmt"
	"time"
)

const (
	// DefaultTimeout bounds a single operation when --timeout is not given.
	DefaultTimeout = 30 * time.Second
	// MaxTimeout is the policy maximum for a single bounded operation. A
	// requested timeout cannot exceed it (see docs/contracts/cli.md).
	MaxTimeout = 10 * time.Minute
)

// Settings is the validated runtime configuration for one invocation.
type Settings struct {
	// Timeout bounds a single operation. It must be positive and must not
	// exceed MaxTimeout.
	Timeout time.Duration
}

// Validate reports whether the settings are within policy. It fails closed:
// non-positive or over-maximum timeouts are rejected rather than clamped.
func (s Settings) Validate() error {
	if s.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive, got %s", s.Timeout)
	}
	if s.Timeout > MaxTimeout {
		return fmt.Errorf("timeout %s exceeds policy maximum %s", s.Timeout, MaxTimeout)
	}
	return nil
}
