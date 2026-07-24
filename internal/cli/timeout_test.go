package cli

import (
	"bytes"
	"context"
	"testing"
)

func TestTimeoutFlagAccepted(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "--timeout", "5s"}, &stdout, &stderr, testBuild())
	if code != int(ExitOK) {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
}

func TestNegativeTimeoutIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "--timeout", "-1s"}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Fatalf("exit code = %d, want %d; stderr=%q", code, int(ExitUsage), stderr.String())
	}
}

func TestOverMaxTimeoutIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "--timeout", "24h"}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Fatalf("exit code = %d, want %d; stderr=%q", code, int(ExitUsage), stderr.String())
	}
}

func TestUnparseableTimeoutIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "--timeout", "notaduration"}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Fatalf("exit code = %d, want %d; stderr=%q", code, int(ExitUsage), stderr.String())
	}
}
