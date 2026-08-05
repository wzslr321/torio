//go:build darwin || linux

package platform

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

const processCancellationHelper = "TORIO_PLATFORM_E2E_PROCESS_HELPER"

func TestConfigureProcessCancellationTerminatesTheWholeGroup(t *testing.T) {
	if os.Getenv(processCancellationHelper) == "1" {
		runProcessCancellationHelper()
		return
	}

	g := NewWithT(t)
	work := t.TempDir()
	ready := filepath.Join(work, "ready")
	termObserved := filepath.Join(work, "term-observed")
	descendantReady := filepath.Join(work, "descendant-ready")
	descendantSurvived := filepath.Join(work, "descendant-survived")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConfigureProcessCancellationTerminatesTheWholeGroup$")
	cmd.Env = append(os.Environ(),
		processCancellationHelper+"=1",
		"TORIO_PROCESS_READY="+ready,
		"TORIO_PROCESS_TERM_OBSERVED="+termObserved,
		"TORIO_PROCESS_DESCENDANT_READY="+descendantReady,
		"TORIO_PROCESS_DESCENDANT_SURVIVED="+descendantSurvived,
	)
	configureProcessCancellation(cmd)
	g.Expect(cmd.Start()).To(Succeed())
	g.Eventually(ready, 2*time.Second, 10*time.Millisecond).Should(BeAnExistingFile())

	cancel()
	g.Expect(cmd.Wait()).To(HaveOccurred())
	g.Eventually(termObserved, 2*time.Second, 10*time.Millisecond).Should(BeAnExistingFile())
	time.Sleep(2 * time.Second)
	g.Expect(descendantSurvived).NotTo(BeAnExistingFile())
}

func runProcessCancellationHelper() {
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	defer signal.Stop(term)

	descendant := exec.Command(os.Args[0], "-test.run=^TestProcessCancellationDescendant$")
	descendant.Env = append(os.Environ(), processCancellationHelper+"=descendant")
	if err := descendant.Start(); err != nil {
		os.Exit(2)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("TORIO_PROCESS_DESCENDANT_READY")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(2)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(os.Getenv("TORIO_PROCESS_READY"), []byte("ready\n"), 0o600); err != nil {
		os.Exit(2)
	}
	<-term
	if err := os.WriteFile(os.Getenv("TORIO_PROCESS_TERM_OBSERVED"), []byte("term\n"), 0o600); err != nil {
		os.Exit(2)
	}
	time.Sleep(5 * time.Second)
}

func TestProcessCancellationDescendant(t *testing.T) {
	if os.Getenv(processCancellationHelper) != "descendant" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	if err := os.WriteFile(os.Getenv("TORIO_PROCESS_DESCENDANT_READY"), []byte("ready\n"), 0o600); err != nil {
		os.Exit(2)
	}
	time.Sleep(1500 * time.Millisecond)
	if err := os.WriteFile(os.Getenv("TORIO_PROCESS_DESCENDANT_SURVIVED"), []byte("bad\n"), 0o600); err != nil {
		os.Exit(2)
	}
}
