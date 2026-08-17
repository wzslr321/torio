//go:build platform_e2e

package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	// cliOperationTimeout is the product policy ceiling passed to Torio. The
	// harness timeout below may be raised independently, but this value must not
	// exceed what the binary accepts.
	cliOperationTimeout = 10 * time.Minute
	commandTimeoutEnv   = "PLATFORM_E2E_COMMAND_TIMEOUT"

	commandFailureOutputLimit = 4096
)

type driver struct {
	binary      string
	artifactDir string
	env         []string
	context     context.Context
	commandWait time.Duration
}

// newDriver builds the CLI harness. Both XDG roots are redirected into the
// suite's own working directory: the config root holds the registry a journey
// writes, and the data root holds what Torio keeps beside it, which is the host
// vault and the bare repository a local project reconciles through (ADR-0025,
// ADR-0029). A journey that wrote either into the operator's own data directory
// would leave a repository behind on every developer machine it ran on.
func newDriver(binary, artifactDir, xdgConfigHome, xdgDataHome string) *driver {
	GinkgoHelper()
	Expect(binary).NotTo(BeEmpty(), "PLATFORM_E2E_TORIO_BIN is required")
	info, err := os.Stat(binary)
	Expect(err).NotTo(HaveOccurred(), "stat installed Torio binary")
	Expect(info.Mode()&0o111).NotTo(BeZero(), "installed Torio binary is not executable")
	Expect(os.MkdirAll(artifactDir, 0o700)).To(Succeed())
	commandWait, err := harnessCommandTimeout(os.Getenv(commandTimeoutEnv))
	Expect(err).NotTo(HaveOccurred(), commandTimeoutEnv)
	return &driver{
		binary:      binary,
		artifactDir: artifactDir,
		env: replaceEnvironment(
			replaceEnvironment(os.Environ(), "XDG_CONFIG_HOME", xdgConfigHome),
			"XDG_DATA_HOME", xdgDataHome),
		context:     context.Background(),
		commandWait: commandWait,
	}
}

// harnessCommandTimeout is the test's patience, not Torio's operation policy.
// Empty keeps the historical ceiling-sized default; an override may exceed it
// without changing the --timeout value sent to the binary.
func harnessCommandTimeout(raw string) (time.Duration, error) {
	if raw == "" {
		return cliOperationTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", commandTimeoutEnv, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", commandTimeoutEnv)
	}
	return d, nil
}

func (d *driver) setContext(ctx context.Context) {
	d.context = ctx
}

func (d *driver) mustRun(label, command string, args ...string) envelope {
	GinkgoHelper()
	got, err := d.run(label, command, args...)
	Expect(err).NotTo(HaveOccurred())
	return got
}

func (d *driver) run(label, command string, args ...string) (envelope, error) {
	ctx, cancel := context.WithTimeout(d.context, d.commandWait)
	defer cancel()

	argv := append([]string{"--json", "--timeout", cliOperationTimeout.String()}, args...)
	cmd := exec.CommandContext(ctx, d.binary, argv...)
	configureProcessCancellation(cmd)
	cmd.Env = d.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	stdoutPath := filepath.Join(d.artifactDir, label+".json")
	stderrPath := filepath.Join(d.artifactDir, label+".stderr.txt")
	if writeErr := writeArtifact(stdoutPath, stdout.Bytes()); writeErr != nil {
		return envelope{}, fmt.Errorf("write %s stdout artifact: %w", label, writeErr)
	}
	if writeErr := writeArtifact(stderrPath, stderr.Bytes()); writeErr != nil {
		return envelope{}, fmt.Errorf("write %s stderr artifact: %w", label, writeErr)
	}
	AddReportEntry(label, fmt.Sprintf("torio command=%s stdout=%s stderr=%s", command, stdoutPath, stderrPath))

	if ctx.Err() != nil {
		return envelope{}, fmt.Errorf("torio %s timed out: %w", command, ctx.Err())
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return envelope{}, fmt.Errorf(
				"torio %s exited %d: %s",
				command,
				exitErr.ExitCode(),
				formatCommandFailure(stderr.String(), stdout.String()),
			)
		}
		return envelope{}, fmt.Errorf("run torio %s: %w", command, err)
	}
	if stderr.Len() != 0 {
		return envelope{}, fmt.Errorf(
			"torio %s wrote unexpected stderr (stdout=%s): %s",
			command,
			truncateString(strings.TrimSpace(stdout.String()), commandFailureOutputLimit),
			truncateString(strings.TrimSpace(stderr.String()), commandFailureOutputLimit),
		)
	}
	got, decodeErr := decodeEnvelope(stdout.Bytes(), command)
	if decodeErr != nil {
		return envelope{}, fmt.Errorf("torio %s: %w", command, decodeErr)
	}
	return got, nil
}

func formatCommandFailure(stderr, stdout string) string {
	trimmedStdout := truncateString(strings.TrimSpace(stdout), commandFailureOutputLimit)
	trimmedStderr := truncateString(strings.TrimSpace(stderr), commandFailureOutputLimit)

	if trimmedStderr == "" && trimmedStdout == "" {
		return "(no stderr/stdout payload)"
	}
	if trimmedStderr == "" {
		return fmt.Sprintf("stderr empty; stdout=%q", trimmedStdout)
	}
	if trimmedStdout == "" {
		return fmt.Sprintf("stderr=%q", trimmedStderr)
	}
	return fmt.Sprintf("stdout=%q; stderr=%q", trimmedStdout, trimmedStderr)
}

func truncateString(body string, limit int) string {
	if len(body) <= limit {
		return body
	}
	return body[:limit] + "..."
}

func expectData(got envelope, expected map[string]any) {
	GinkgoHelper()
	for path, want := range expected {
		value, found := nestedValue(got.Data, strings.Split(path, "."))
		Expect(found).To(BeTrue(), "data.%s is missing", path)
		Expect(value).To(Equal(want), "data.%s", path)
	}
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func writeArtifact(path string, body []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".artifact-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
