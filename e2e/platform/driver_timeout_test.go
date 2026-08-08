//go:build platform_e2e

package platform

import (
	"testing"
	"time"
)

func TestHarnessCommandTimeout(t *testing.T) {
	if cliOperationTimeout != 10*time.Minute {
		t.Fatalf("CLI timeout = %s, want the 10m product policy ceiling", cliOperationTimeout)
	}

	tests := []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "default stays at the CLI policy ceiling", want: 10 * time.Minute},
		{name: "harness may wait past the CLI ceiling", raw: "20m", want: 20 * time.Minute},
		{name: "shorter patience is allowed", raw: "30s", want: 30 * time.Second},
		{name: "invalid", raw: "eventually", wantErr: true},
		{name: "zero", raw: "0s", wantErr: true},
		{name: "negative", raw: "-1s", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := harnessCommandTimeout(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("harnessCommandTimeout(%q) accepted an invalid duration", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("harnessCommandTimeout(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("harnessCommandTimeout(%q) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}
