//go:build unix

// These tests drive real process groups and terminal-generated signals, so
// they are unix-only — the same boundary procgroup_unix_test.go draws. The
// runner itself builds everywhere.

package execx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wzslr321/torio/internal/redact"
)

// InteractiveExecRunner must satisfy the InteractiveRunner interface.
var _ InteractiveRunner = (*InteractiveExecRunner)(nil)

// TestInteractiveHelperProcess is not a real test: when re-executed with
// HB_INTERACTIVE_MODE set, it acts as a controllable child process. It is the
// only honest way to observe a runner whose whole contract is to inherit the
// real os.Stdin/os.Stdout/os.Stderr — in "parent" mode this process runs the
// production runner, so the grandchild it spawns can only see the descriptors
// the runner passed on.
func TestInteractiveHelperProcess(t *testing.T) {
	switch os.Getenv("HB_INTERACTIVE_MODE") {
	case "":
		return // ordinary test run: no-op
	case "parent":
		// Everything this process logs is captured, so the caller can prove
		// the runner logs nothing at all about the session.
		var logged bytes.Buffer
		slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))

		// Run the production runner over a grandchild and mirror its outcome
		// onto this process's exit status, so the caller observes exit-code
		// propagation at the process level, not through a test assertion.
		err := (&InteractiveExecRunner{}).RunInteractive(context.Background(), interactiveChildCommand())

		if path := os.Getenv("HB_INTERACTIVE_LOGFILE"); path != "" {
			if writeErr := os.WriteFile(path, logged.Bytes(), 0o600); writeErr != nil {
				os.Exit(104)
			}
		}
		var exitErr *ExitError
		switch {
		case err == nil:
			os.Exit(0)
		case errors.As(err, &exitErr):
			os.Exit(exitErr.Code)
		default:
			os.Exit(101)
		}
	case "echo":
		// Grandchild: prove all three inherited streams by echoing what
		// arrived on stdin to both stdout and stderr.
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(102)
		}
		fmt.Fprint(os.Stdout, "OUT:"+string(data))
		fmt.Fprint(os.Stderr, "ERR:"+string(data))
		os.Exit(0)
	case "exit":
		code, err := strconv.Atoi(os.Getenv("HB_INTERACTIVE_EXIT"))
		if err != nil {
			os.Exit(103)
		}
		os.Exit(code)
	case "secret":
		// Grandchild: emit a canary on both streams. It must reach the outer
		// process untouched — proof that nothing in between retained it.
		secret := os.Getenv("HB_INTERACTIVE_SECRET")
		fmt.Fprint(os.Stdout, secret)
		fmt.Fprint(os.Stderr, secret)
		os.Exit(0)
	case "sigwait":
		// Grandchild: announce readiness, then exit 42 on the first SIGINT.
		// It only ever sees that signal if it shares the process group the
		// terminal signals — i.e. if the runner did not isolate it.
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		fmt.Fprintln(os.Stdout, "READY")
		<-ch
		os.Exit(42)
	default:
		os.Exit(3)
	}
}

// interactiveChildCommand builds the grandchild command for helper "parent"
// mode: this same test binary, in the mode named by HB_INTERACTIVE_CHILD.
func interactiveChildCommand() InteractiveCommand {
	env := append(os.Environ(), "HB_INTERACTIVE_MODE="+os.Getenv("HB_INTERACTIVE_CHILD"))
	return InteractiveCommand{
		Name: os.Args[0],
		Args: []string{"-test.run=^TestInteractiveHelperProcess$"},
		Env:  env,
	}
}

// helperOutcome is what the outer test observes about a helper run: the bytes
// that reached the helper's own stdout/stderr and its exit status.
type helperOutcome struct {
	stdout   string
	stderr   string
	exitCode int
}

// runInteractiveHelper re-executes this test binary in "parent" mode with the
// given grandchild mode, feeding stdin and collecting the streams that the
// production runner is supposed to hand straight through.
func runInteractiveHelper(t *testing.T, childMode, stdin string, extraEnv ...string) helperOutcome {
	t.Helper()

	c := exec.Command(os.Args[0], "-test.run=^TestInteractiveHelperProcess$")
	c.Env = append(os.Environ(),
		"HB_INTERACTIVE_MODE=parent",
		"HB_INTERACTIVE_CHILD="+childMode,
	)
	c.Env = append(c.Env, extraEnv...)
	var out, errBuf bytes.Buffer
	c.Stdin = strings.NewReader(stdin)
	c.Stdout = &out
	c.Stderr = &errBuf

	err := c.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("helper process failed to run: %v", err)
	}
	return helperOutcome{stdout: out.String(), stderr: errBuf.String(), exitCode: c.ProcessState.ExitCode()}
}

// TestRunInteractivePassesParentStdioStraightThrough proves the child inherits
// the parent's real standard input, output and error: the grandchild reads the
// bytes the outer test wrote to the helper's stdin and writes its answer onto
// the helper's own stdout and stderr. Nothing in between captures them.
func TestRunInteractivePassesParentStdioStraightThrough(t *testing.T) {
	const payload = "operator-typed-this\n"

	got := runInteractiveHelper(t, "echo", payload)

	if got.exitCode != 0 {
		t.Fatalf("helper exit = %d, want 0 (stderr=%q)", got.exitCode, got.stderr)
	}
	if want := "OUT:" + payload; got.stdout != want {
		t.Errorf("parent stdout = %q, want %q", got.stdout, want)
	}
	if want := "ERR:" + payload; got.stderr != want {
		t.Errorf("parent stderr = %q, want %q", got.stderr, want)
	}
}

// TestRunInteractiveKeepsChildInTheTerminalForegroundGroup proves the two
// coupled behaviors that make Ctrl-C work inside an operator session: the
// child stays in the parent's process group, so a terminal-generated SIGINT
// reaches it directly, and the parent survives the same signal instead of
// abandoning a live session. The test stands in for the terminal by giving the
// helper its own process group and signalling that group.
func TestRunInteractiveKeepsChildInTheTerminalForegroundGroup(t *testing.T) {
	c := exec.Command(os.Args[0], "-test.run=^TestInteractiveHelperProcess$")
	c.Env = append(os.Environ(),
		"HB_INTERACTIVE_MODE=parent",
		"HB_INTERACTIVE_CHILD=sigwait",
	)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := c.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	// Backstop: never leave a hung session behind, and never hang the suite.
	guard := time.AfterFunc(20*time.Second, func() { _ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL) })
	defer guard.Stop()

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("reading grandchild readiness: %v", err)
	}
	if strings.TrimSpace(line) != "READY" {
		t.Fatalf("grandchild announced %q, want READY", strings.TrimSpace(line))
	}

	if err := syscall.Kill(-c.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatalf("signalling the foreground group: %v", err)
	}

	var exitErr *exec.ExitError
	if err := c.Wait(); err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("waiting for helper: %v", err)
	}
	if code := c.ProcessState.ExitCode(); code != 42 {
		t.Errorf("helper exit = %d, want 42 (grandchild handled SIGINT and the parent survived to report it)", code)
	}
}

// TestRunInteractiveNeitherCapturesNorLogsChildOutput proves the contract that
// makes this runner different from ExecRunner: the child's output goes to the
// operator's terminal and nowhere else. A canary emitted by the grandchild
// arrives byte-for-byte on the parent's streams (nothing intercepted it) while
// the session produces no log record at all — not the environment, not a line
// of output (AGENTS §6).
func TestRunInteractiveNeitherCapturesNorLogsChildOutput(t *testing.T) {
	const secret = "swordfish-6b1e-canary"
	logPath := filepath.Join(t.TempDir(), "session.log")

	got := runInteractiveHelper(t, "secret", "",
		"HB_INTERACTIVE_SECRET="+secret,
		"HB_INTERACTIVE_LOGFILE="+logPath,
	)

	if got.exitCode != 0 {
		t.Fatalf("helper exit = %d, want 0", got.exitCode)
	}
	if got.stdout != secret || got.stderr != secret {
		t.Errorf("child output did not pass through untouched: stdout=%q stderr=%q", got.stdout, got.stderr)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading captured log: %v", err)
	}
	if len(bytes.TrimSpace(logged)) != 0 {
		t.Errorf("the runner logged %q; an interactive session logs neither environment nor output", logged)
	}
}

// TestRunInteractiveRejectsEmptyName proves an empty executable is rejected by
// this package before it reaches os/exec, with our own diagnostic rather than
// an opaque runtime error, mirroring ExecRunner.Run.
func TestRunInteractiveRejectsEmptyName(t *testing.T) {
	r := &InteractiveExecRunner{}
	err := r.RunInteractive(context.Background(), InteractiveCommand{Args: []string{"-A"}})
	if err == nil {
		t.Fatalf("expected an error for an empty command name")
	}
	if !strings.Contains(err.Error(), "execx:") {
		t.Errorf("error = %q, want this package's own rejection", err)
	}
}

// TestRunInteractiveRedactsDiagnosticsAndOmitsArgsAndEnv proves the one
// failure path that produces a message of our own — a command that could not
// start — names the executable and nothing else, and passes even that through
// redaction (AGENTS §6). The argument array and the environment of an operator
// session carry credentials by construction; they never reach a diagnostic.
func TestRunInteractiveRedactsDiagnosticsAndOmitsArgsAndEnv(t *testing.T) {
	const argSecret = "swordfish-6b1e-canary"
	const envSecret = "hunter-9c4d-canary"
	// A token-shaped literal inside the executable path: the default patterns
	// must mask a known secret shape wherever it appears.
	name := "/nonexistent/ghp_" + strings.Repeat("A", 24) + "/ssh"

	r := &InteractiveExecRunner{Redactor: redact.New(argSecret, envSecret)}
	err := r.RunInteractive(context.Background(), InteractiveCommand{
		Name: name,
		Args: []string{"--token", argSecret},
		Env:  []string{"GITHUB_TOKEN=" + envSecret},
	})
	if err == nil {
		t.Fatalf("expected a start error for a missing executable, got nil")
	}

	msg := err.Error()
	for label, forbidden := range map[string]string{
		"argument secret":    argSecret,
		"environment secret": envSecret,
		"argument name":      "--token",
		"environment name":   "GITHUB_TOKEN",
		"token-shaped path":  "ghp_",
	} {
		if strings.Contains(msg, forbidden) {
			t.Errorf("diagnostic leaked the %s: %q", label, msg)
		}
	}
	if !strings.Contains(msg, redact.Placeholder) {
		t.Errorf("diagnostic was not redacted: %q", msg)
	}
}

// TestRunInteractiveReportsCancellationNotAnExitCode proves a session torn
// down by context cancellation is reported as a cancellation, not as a remote
// exit status. A killed session never "exited"; mapping it onto an exit code
// would let a caller report a remote failure that never happened.
func TestRunInteractiveReportsCancellationNotAnExitCode(t *testing.T) {
	r := &InteractiveExecRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	err := r.RunInteractive(ctx, InteractiveCommand{Name: "sleep", Args: []string{"30"}})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("RunInteractive = nil, want a cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		t.Errorf("cancellation surfaced as exit status %d; a killed session did not exit", exitErr.Code)
	}
	if elapsed > 5*time.Second {
		t.Errorf("cancellation took %v; the child was not terminated promptly", elapsed)
	}
}

// TestRunInteractivePropagatesChildExitCode proves the interactive runner
// reports the child's exit status as a typed error instead of a captured
// Result: a clean exit is nil, a non-zero exit is an *ExitError carrying the
// exact code so the CLI can exit with it.
func TestRunInteractivePropagatesChildExitCode(t *testing.T) {
	r := &InteractiveExecRunner{}

	if err := r.RunInteractive(context.Background(), InteractiveCommand{Name: "true"}); err != nil {
		t.Fatalf("RunInteractive(true) = %v, want nil", err)
	}

	err := r.RunInteractive(context.Background(), InteractiveCommand{Name: "false"})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("RunInteractive(false) = %v, want an *ExitError", err)
	}
	if exitErr.Code != 1 {
		t.Errorf("ExitError.Code = %d, want 1", exitErr.Code)
	}

	// End to end, the way the CLI will use it: a remote exit code survives the
	// runner and becomes the caller's own exit status.
	if got := runInteractiveHelper(t, "exit", "", "HB_INTERACTIVE_EXIT=7"); got.exitCode != 7 {
		t.Errorf("process exit = %d, want the child's 7", got.exitCode)
	}
}
