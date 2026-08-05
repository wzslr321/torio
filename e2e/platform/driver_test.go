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

const commandTimeout = 10 * time.Minute
const commandFailureOutputLimit = 4096

type driver struct {
	binary      string
	artifactDir string
	env         []string
	context     context.Context
}

func newDriver(binary, artifactDir, xdgConfigHome string) *driver {
	GinkgoHelper()
	Expect(binary).NotTo(BeEmpty(), "PLATFORM_E2E_TORIO_BIN is required")
	info, err := os.Stat(binary)
	Expect(err).NotTo(HaveOccurred(), "stat installed Torio binary")
	Expect(info.Mode()&0o111).NotTo(BeZero(), "installed Torio binary is not executable")
	Expect(os.MkdirAll(artifactDir, 0o700)).To(Succeed())
	return &driver{
		binary:      binary,
		artifactDir: artifactDir,
		env:         replaceEnvironment(os.Environ(), "XDG_CONFIG_HOME", xdgConfigHome),
		context:     context.Background(),
	}
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
	ctx, cancel := context.WithTimeout(d.context, commandTimeout)
	defer cancel()

	argv := append([]string{"--json", "--timeout", "10m"}, args...)
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
