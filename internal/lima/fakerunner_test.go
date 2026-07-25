package lima

import (
	"context"
	"sync"

	"hermes-box.local/hb/internal/execx"
)

// recordedCall captures one Run invocation: the command it received and
// whether ctx was already done when Run was invoked, so tests can assert
// argv shape and context propagation without a real process.
type recordedCall struct {
	cmd    execx.Command
	ctxErr error
}

// fakeRunner is a deterministic, local execx.Runner test double. It never
// spawns a real process, so it can express any limactl behavior (success,
// failure, malformed output, timeout) without a real Lima VM.
type fakeRunner struct {
	mu    sync.Mutex
	calls []recordedCall

	// script supplies one response per call, consumed in order. If it is
	// shorter than the number of calls, respond is used for the remainder.
	script []scriptedResponse
	// respond, if set, computes a response for any call not covered by
	// script. Useful for context/timeout tests that need to inspect ctx.
	respond func(ctx context.Context, cmd execx.Command) (execx.Result, error)
}

// scriptedResponse is one canned (Result, error) pair.
type scriptedResponse struct {
	result execx.Result
	err    error
}

func (f *fakeRunner) Run(ctx context.Context, cmd execx.Command) (execx.Result, error) {
	f.mu.Lock()
	idx := len(f.calls)
	f.calls = append(f.calls, recordedCall{cmd: cmd, ctxErr: ctx.Err()})
	f.mu.Unlock()

	if idx < len(f.script) {
		return f.script[idx].result, f.script[idx].err
	}
	if f.respond != nil {
		return f.respond(ctx, cmd)
	}
	return execx.Result{}, nil
}

func (f *fakeRunner) callArgs(i int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i].cmd.Args
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func stdoutResult(s string) execx.Result {
	return execx.Result{ExitCode: 0, Stdout: []byte(s)}
}

func exitResult(code int, stdout, stderr string) execx.Result {
	return execx.Result{ExitCode: code, Stdout: []byte(stdout), Stderr: []byte(stderr)}
}
