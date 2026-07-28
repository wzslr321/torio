package config

import (
	"testing"
	"time"
)

func TestValidateRejectsNonPositiveTimeout(t *testing.T) {
	for _, d := range []time.Duration{0, -1 * time.Second, -time.Millisecond} {
		s := Settings{Timeout: d}
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%v) = nil, want error", d)
		}
	}
}

func TestValidateRejectsTimeoutAbovePolicyMax(t *testing.T) {
	s := Settings{Timeout: MaxTimeout + time.Second}
	if err := s.Validate(); err == nil {
		t.Errorf("Validate(%v) = nil, want error (exceeds policy max %v)", s.Timeout, MaxTimeout)
	}
}

func TestValidateAcceptsInRangeTimeout(t *testing.T) {
	for _, d := range []time.Duration{time.Millisecond, 5 * time.Second, MaxTimeout} {
		s := Settings{Timeout: d}
		if err := s.Validate(); err != nil {
			t.Errorf("Validate(%v) = %v, want nil", d, err)
		}
	}
}
