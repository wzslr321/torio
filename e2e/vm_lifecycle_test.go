//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	fakeMarkerEnv       = "TORIO_E2E_FAKE_LIMACTL"
	fakeStateEnv        = "TORIO_E2E_LIMA_STATE"
	fakeCallsEnv        = "TORIO_E2E_LIMA_CALLS"
	fakeTemplateEnv     = "TORIO_E2E_TEMPLATE_CAPTURE"
	fakeForwardAgentEnv = "TORIO_E2E_FORWARD_AGENT"

	promotedImageRelease = "https://cloud-images.ubuntu.com/releases/noble/release-20260705/"
)

// The instance pins the fake `limactl` reports must be the ones the compiled
// CLI expects, and the CLI derives them from the host it runs on
// (internal/lima.profiles). This module cannot import that table — the e2e
// suites are their own module, and `internal/` does not cross a module boundary
// — so the values are restated here and selected the same way.
//
// A single hardcoded pair would make this suite pass on one host and fail on
// the other with a message about image digests, which says nothing about what
// is actually wrong.
type hostPins struct {
	vmType      string
	arch        string
	imageURL    string
	imageDigest string
}

func pinsForHost() (hostPins, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return hostPins{
			vmType:      "vz",
			arch:        "aarch64",
			imageURL:    promotedImageRelease + "ubuntu-24.04-server-cloudimg-arm64.img",
			imageDigest: "sha256:7df0201546f75b8bcc1044594c806c35749421ad3c9bc1be2a3ab806cfae39cc",
		}, nil
	case "linux/amd64":
		return hostPins{
			vmType:      "qemu",
			arch:        "x86_64",
			imageURL:    promotedImageRelease + "ubuntu-24.04-server-cloudimg-amd64.img",
			imageDigest: "sha256:ffe6203da54deeb6db5d2a98a83f9ec8e55f149d3f7ba622e1abe5fa966ee3d6",
		}, nil
	default:
		return hostPins{}, fmt.Errorf("no Torio instance pins for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

var torioBinary string

type testContext interface {
	Helper()
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
}

func TestMain(m *testing.M) {
	if os.Getenv(fakeMarkerEnv) == "1" {
		os.Exit(runFakeLimactl(os.Args[1:]))
	}

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "resolve E2E source path")
		os.Exit(1)
	}
	root := filepath.Dir(filepath.Dir(source))
	buildDir, err := os.MkdirTemp("", "torio-e2e-build-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create E2E build directory: %v\n", err)
		os.Exit(1)
	}

	torioBinary = filepath.Join(buildDir, "torio")
	build := exec.Command("go", "build", "-trimpath", "-o", torioBinary, "./cmd/torio")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build torio E2E binary: %v\n%s", err, output)
		_ = os.RemoveAll(buildDir)
		os.Exit(1)
	}

	code := m.Run()
	if err := os.RemoveAll(buildDir); err != nil {
		fmt.Fprintf(os.Stderr, "remove E2E build directory: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func TestCompiledCLISmoke(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Compiled CLI Smoke Suite")
}

var _ = Describe("the compiled Torio CLI", func() {
	It("drives the VM lifecycle through a strict process fake", func() {
		t := GinkgoT()
		env := newEnvironment(t, false)

		status := env.runJSON(t, "vm", "status", "--json")
		assertData(t, status, "vm.status", map[string]any{
			"name":  "torio-e2e",
			"state": "not_found",
		})

		pins, err := pinsForHost()
		if err != nil {
			t.Fatalf("%v", err)
		}
		created := env.runJSON(t, "--json", "vm", "init")
		assertData(t, created, "vm.init", map[string]any{
			"name":           "torio-e2e",
			"created":        true,
			"unchanged":      false,
			"image_location": pins.imageURL,
			"image_digest":   pins.imageDigest,
			"next_step":      "torio vm start",
		})

		unchanged := env.runJSON(t, "vm", "init", "--json")
		assertData(t, unchanged, "vm.init", map[string]any{
			"created":   false,
			"unchanged": true,
		})

		started := env.runJSON(t, "vm", "start", "--json")
		assertData(t, started, "vm.start", map[string]any{
			"name":  "torio-e2e",
			"state": "running",
		})
		assertData(t, env.runJSON(t, "--json", "vm", "status"), "vm.status", map[string]any{
			"state": "running",
		})

		stopped := env.runJSON(t, "--json", "vm", "stop")
		assertData(t, stopped, "vm.stop", map[string]any{
			"name":  "torio-e2e",
			"state": "stopped",
		})
		assertData(t, env.runJSON(t, "vm", "stop", "--json"), "vm.stop", map[string]any{
			"state": "stopped",
		})

		calls := env.calls(t)
		assertCommandSequence(t, calls, []string{
			"list",
			"list", "create", "list",
			"list",
			"list", "start", "list", "shell",
			"list",
			"list", "stop", "list",
			"list",
		})
		assertCommandCount(t, calls, "create", 1)
		assertCommandCount(t, calls, "start", 1)
		assertCommandCount(t, calls, "stop", 1)
		assertCommandCount(t, calls, "shell", 1)
		for _, call := range calls {
			if slices.Contains(call, "delete") || slices.Contains(call, "--force") {
				t.Fatalf("lifecycle used a destructive argument: %v", call)
			}
		}

		template, err := os.ReadFile(env.templatePath)
		if err != nil {
			t.Fatalf("read captured Lima template: %v", err)
		}
		for _, required := range []string{
			"mounts: []",
			"forwardAgent: false",
			"vmType: " + pins.vmType,
			"arch: " + pins.arch,
			pins.imageURL,
			pins.imageDigest,
		} {
			if !bytes.Contains(template, []byte(required)) {
				t.Errorf("captured template does not contain %q", required)
			}
		}
	})

	It("fails closed for persistent SSH agent forwarding", func() {
		t := GinkgoT()
		env := newEnvironment(t, true)
		if err := os.WriteFile(env.statePath, []byte("Stopped\n"), 0o600); err != nil {
			t.Fatalf("seed fake Lima state: %v", err)
		}

		result := env.run(t, "--json", "vm", "init")
		Expect(result.exitCode).To(Equal(6), "stdout=%q stderr=%q", result.stdout, result.stderr)
		envelope := decodeEnvelope(t, result.stdout)
		if envelope.OK || envelope.Command != "vm.init" || envelope.Error == nil || envelope.Error.Code != "INCOMPATIBLE" {
			code := "<nil>"
			if envelope.Error != nil {
				code = envelope.Error.Code
			}
			t.Fatalf("unexpected failure envelope: ok=%v command=%q code=%q", envelope.OK, envelope.Command, code)
		}
		assertCommandCount(t, env.calls(t), "create", 0)
	})
})

type environment struct {
	env          []string
	statePath    string
	callsPath    string
	templatePath string
}

func newEnvironment(t testContext, forwardAgent bool) environment {
	t.Helper()
	root, err := os.MkdirTemp("", "torio-compiled-cli-smoke-")
	if err != nil {
		t.Fatalf("create smoke directory: %v", err)
	}
	DeferCleanup(func() {
		Expect(os.RemoveAll(root)).To(Succeed())
	})
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatalf("create fake bin directory: %v", err)
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve E2E test binary: %v", err)
	}
	if err := os.Symlink(testBinary, filepath.Join(binDir, "limactl")); err != nil {
		t.Fatalf("install fake limactl: %v", err)
	}

	statePath := filepath.Join(root, "lima-state")
	callsPath := filepath.Join(root, "lima-calls.jsonl")
	templatePath := filepath.Join(root, "template.yaml")
	env := environmentWithout(
		fakeMarkerEnv,
		fakeStateEnv,
		fakeCallsEnv,
		fakeTemplateEnv,
		fakeForwardAgentEnv,
		"TORIO_INSTANCE",
		"XDG_CONFIG_HOME",
		"HOME",
		"PATH",
	)
	env = append(env,
		fakeMarkerEnv+"=1",
		fakeStateEnv+"="+statePath,
		fakeCallsEnv+"="+callsPath,
		fakeTemplateEnv+"="+templatePath,
		fakeForwardAgentEnv+"="+fmt.Sprint(forwardAgent),
		"TORIO_INSTANCE=torio-e2e",
		"XDG_CONFIG_HOME="+filepath.Join(root, "xdg"),
		"HOME="+filepath.Join(root, "home"),
		"PATH="+binDir,
	)
	return environment{env: env, statePath: statePath, callsPath: callsPath, templatePath: templatePath}
}

func environmentWithout(keys ...string) []string {
	excluded := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		excluded[key] = struct{}{}
	}
	filtered := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, found := excluded[key]; !found {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

type commandResult struct {
	exitCode int
	stdout   string
	stderr   string
}

func (e environment) run(t testContext, args ...string) commandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, torioBinary, args...)
	cmd.Env = e.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("torio %v timed out: %v", args, ctx.Err())
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run torio %v: %v", args, err)
		}
		exitCode = exitErr.ExitCode()
	}
	return commandResult{exitCode: exitCode, stdout: stdout.String(), stderr: stderr.String()}
}

func (e environment) runJSON(t testContext, args ...string) envelope {
	t.Helper()
	result := e.run(t, args...)
	if result.exitCode != 0 {
		t.Fatalf("torio %v exit = %d; stdout=%q stderr=%q", args, result.exitCode, result.stdout, result.stderr)
	}
	if result.stderr != "" {
		t.Fatalf("torio %v wrote unexpected stderr: %q", args, result.stderr)
	}
	return decodeEnvelope(t, result.stdout)
}

func (e environment) calls(t testContext) [][]string {
	t.Helper()
	body, err := os.ReadFile(e.callsPath)
	if err != nil {
		t.Fatalf("read fake Lima calls: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	var calls [][]string
	for {
		var call []string
		if err := dec.Decode(&call); err != nil {
			if err == io.EOF {
				return calls
			}
			t.Fatalf("decode fake Lima calls: %v", err)
		}
		calls = append(calls, call)
	}
}

type envelope struct {
	SchemaVersion string         `json:"schema_version"`
	OK            bool           `json:"ok"`
	Command       string         `json:"command"`
	Data          map[string]any `json:"data"`
	Error         *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func decodeEnvelope(t testContext, stdout string) envelope {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var got envelope
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("stdout is not an envelope: %v; stdout=%q", err, stdout)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		t.Fatalf("stdout contains more than one JSON document: %v; stdout=%q", err, stdout)
	}
	if got.SchemaVersion != "1" {
		t.Fatalf("schema_version = %q, want 1", got.SchemaVersion)
	}
	return got
}

func assertData(t testContext, got envelope, command string, want map[string]any) {
	t.Helper()
	if !got.OK || got.Command != command || got.Error != nil {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	for key, value := range want {
		if got.Data[key] != value {
			t.Errorf("%s data[%q] = %#v, want %#v", command, key, got.Data[key], value)
		}
	}
}

func assertCommandCount(t testContext, calls [][]string, command string, want int) {
	t.Helper()
	got := 0
	for _, call := range calls {
		if len(call) > 0 && call[0] == command {
			got++
		}
	}
	if got != want {
		t.Fatalf("fake limactl %q call count = %d, want %d; calls=%v", command, got, want, calls)
	}
}

func assertCommandSequence(t testContext, calls [][]string, want []string) {
	t.Helper()
	got := make([]string, 0, len(calls))
	for _, call := range calls {
		if len(call) == 0 {
			t.Fatalf("fake limactl recorded an empty argv: %v", calls)
		}
		got = append(got, call[0])
	}
	if !slices.Equal(got, want) {
		t.Fatalf("fake limactl command sequence = %v, want %v; calls=%v", got, want, calls)
	}
}

func runFakeLimactl(args []string) int {
	if err := appendCall(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "missing limactl command")
		return 2
	}

	switch args[0] {
	case "list":
		if !slices.Equal(args, []string{"list", "--json", "--tty=false"}) {
			return unexpectedFakeArgs(args)
		}
		return fakeList()
	case "create":
		if len(args) != 4 || args[1] != "--name="+os.Getenv("TORIO_INSTANCE") || args[2] != "--tty=false" {
			return unexpectedFakeArgs(args)
		}
		return fakeCreate(args)
	case "start":
		if !slices.Equal(args, []string{"start", os.Getenv("TORIO_INSTANCE"), "--tty=false"}) {
			return unexpectedFakeArgs(args)
		}
		return transitionFakeState("Stopped", "Running")
	case "stop":
		if !slices.Equal(args, []string{"stop", os.Getenv("TORIO_INSTANCE"), "--tty=false"}) {
			return unexpectedFakeArgs(args)
		}
		return transitionFakeState("Running", "Stopped")
	case "shell":
		if !slices.Equal(args, []string{"shell", "--reconnect", "--tty=false", "--workdir", "/", os.Getenv("TORIO_INSTANCE"), "--", "true"}) {
			return unexpectedFakeArgs(args)
		}
		return requireFakeState("Running")
	default:
		return unexpectedFakeArgs(args)
	}
}

func unexpectedFakeArgs(args []string) int {
	fmt.Fprintf(os.Stderr, "unexpected fake limactl argv: %v\n", args)
	return 2
}

func appendCall(args []string) error {
	body, err := json.Marshal(args)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(os.Getenv(fakeCallsEnv), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, writeErr := file.Write(append(body, '\n')); writeErr != nil {
		return writeErr
	}
	return nil
}

func fakeList() int {
	body, err := os.ReadFile(os.Getenv(fakeStateEnv))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	forwardAgent := os.Getenv(fakeForwardAgentEnv) == "true"
	pins, err := pinsForHost()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	record := map[string]any{
		"name":   os.Getenv("TORIO_INSTANCE"),
		"status": strings.TrimSpace(string(body)),
		"config": map[string]any{
			"vmType": pins.vmType,
			"arch":   pins.arch,
			"images": []map[string]string{{
				"location": pins.imageURL,
				"digest":   pins.imageDigest,
			}},
			"mounts": []any{},
			"ssh":    map[string]bool{"forwardAgent": forwardAgent},
		},
	}
	if err := json.NewEncoder(os.Stdout).Encode(record); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func fakeCreate(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "create missing template path")
		return 2
	}
	templatePath := args[len(args)-1]
	body, err := os.ReadFile(templatePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(os.Getenv(fakeTemplateEnv), body, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	state, err := readFakeState()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if state != "" {
		fmt.Fprintf(os.Stderr, "fake create requires absent instance, got state %q\n", state)
		return 1
	}
	return writeFakeState("Stopped")
}

func transitionFakeState(from, to string) int {
	state, err := readFakeState()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if state != from {
		fmt.Fprintf(os.Stderr, "fake transition requires state %q, got %q\n", from, state)
		return 1
	}
	return writeFakeState(to)
}

func requireFakeState(want string) int {
	state, err := readFakeState()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if state != want {
		fmt.Fprintf(os.Stderr, "fake command requires state %q, got %q\n", want, state)
		return 1
	}
	return 0
}

func readFakeState() (string, error) {
	body, err := os.ReadFile(os.Getenv(fakeStateEnv))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func writeFakeState(state string) int {
	if err := os.WriteFile(os.Getenv(fakeStateEnv), []byte(state+"\n"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
