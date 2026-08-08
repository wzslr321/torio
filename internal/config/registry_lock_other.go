//go:build !darwin && !linux

package config

func withAdvisoryLock(_ string, update func() error) error { return update() }
