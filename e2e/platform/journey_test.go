//go:build platform_e2e

package platform

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	defaultFixtureRemote = "https://github.com/octocat/Hello-World.git"
	defaultFixtureCommit = "7fd1a60b01f91b314f59955a4e4d4e80d8edf11d"
	// The exercise is parameterized by the agent identity, which on both process
	// backends is also the name a session reports in the process table. The helper
	// walks up to the nearest ancestor with that name, so a run under the wrong
	// one would fail to find a waiting process and prove nothing.
	waitingHelperExercise = `
import ctypes,json,os,subprocess

agent="%[1]s"
helper="/usr/local/bin/torio-waiting-marker"
marker="/home/%[1]s/.torio-waiting.json"

def invoke(action, session_id):
    pid=os.fork()
    if pid == 0:
        ctypes.CDLL(None).prctl(15, agent.encode(), 0, 0, 0)
        result=subprocess.run(
            [helper, action],
            input=json.dumps({"session_id": session_id}),
            text=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        os._exit(result.returncode)
    return pid

def complete(action, ids):
    children=[invoke(action, session_id) for session_id in ids]
    for child in children:
        _, status=os.waitpid(child, 0)
        if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
            raise SystemExit(1)

complete("set", ["e2e-a", "e2e-b"])
with open(marker, encoding="utf-8") as handle:
    waits=json.load(handle)["waits"]
if sorted(wait["session_id"] for wait in waits) != ["e2e-a", "e2e-b"]:
    raise SystemExit(2)

complete("clear", ["e2e-a", "e2e-b"])
with open(marker, encoding="utf-8") as handle:
    if json.load(handle) != {"schema_version": "2", "waits": []}:
        raise SystemExit(3)
`
)

// The journey splits at the hypervisor boundary. Everything up to and including
// `torio vm init` is host work — real release tarball, real install, real
// limactl, real image pin — and runs on any supported host. Everything from
// `torio vm start` on needs a hypervisor the host is allowed to use: Apple's
// Virtualization framework on macOS, KVM on Linux.
//
// GitHub-hosted macOS runners are themselves VMs without nested virtualization,
// so they serve the host stage only. Hosted Linux runners expose /dev/kvm, so
// they serve both — which is why the guest stage is gateable at all.
const (
	hostStage  = "host"
	guestStage = "guest"
)

var _ = Describe("the release-shaped Torio product journey", Ordered, func() {
	var (
		torio           *driver
		workDir         string
		fixtureDir      string
		artifactDir     string
		instanceName    string
		fixtureRemote   string
		fixtureCommit   string
		ownsInstance    bool
		externalCleanup bool
		repositoryRoot  string
		profile         backendProfile
	)

	BeforeAll(func(_ SpecContext) {
		Expect(os.Getenv("PLATFORM_E2E_RUN")).To(Equal("1"), "run through make platform-e2e")
		// The supported host matrix, restated: this module cannot import
		// internal/lima.profiles across the module boundary. Running the journey
		// on an unsupported host would exercise a CLI that refuses every command,
		// and report it as a product failure.
		Expect(supportedHost()).To(BeTrue(),
			"real platform E2E requires a supported host (darwin/arm64 or linux/amd64), got %s/%s",
			runtime.GOOS, runtime.GOARCH)
		_, err := exec.LookPath("limactl")
		Expect(err).NotTo(HaveOccurred(), "real limactl is required")

		profile = profileFor(journeyBackend())
		instanceName = os.Getenv("TORIO_INSTANCE")
		Expect(instanceName).NotTo(BeEmpty(), "TORIO_INSTANCE is required")
		artifactDir = os.Getenv("PLATFORM_E2E_ARTIFACT_DIR")
		Expect(artifactDir).NotTo(BeEmpty(), "PLATFORM_E2E_ARTIFACT_DIR is required")
		binary := os.Getenv("PLATFORM_E2E_TORIO_BIN")
		expectedVersion := os.Getenv("PLATFORM_E2E_EXPECTED_VERSION")
		Expect(expectedVersion).NotTo(BeEmpty(), "PLATFORM_E2E_EXPECTED_VERSION is required")

		workDir, err = os.MkdirTemp("", "torio-platform-e2e-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(workDir)).To(Succeed())
		})

		fixtureDir = filepath.Join(workDir, "brain-fixture")
		Expect(os.MkdirAll(fixtureDir, 0o700)).To(Succeed())
		Expect(os.WriteFile(
			filepath.Join(fixtureDir, "README.md"),
			[]byte("# Torio platform E2E\n\nThis note crossed the real Lima transport.\n"),
			0o600,
		)).To(Succeed())

		fixtureRemote = environmentOr("PLATFORM_E2E_REMOTE", defaultFixtureRemote)
		fixtureCommit = environmentOr("PLATFORM_E2E_FIXTURE_COMMIT", defaultFixtureCommit)
		repositoryRoot = resolveRepositoryRoot()
		externalCleanup = os.Getenv("PLATFORM_E2E_EXTERNAL_CLEANUP") == "1"
		torio = newDriver(binary, artifactDir, filepath.Join(workDir, "xdg"))

		captureTechnicalVersions(artifactDir)
		exists, listErr := limaInstanceExists(instanceName)
		Expect(listErr).NotTo(HaveOccurred(), "read Lima instance state")
		Expect(exists).To(BeFalse(), "refusing to touch pre-existing Lima instance %s", instanceName)

		// The caller owns teardown when it can also collect the Lima host-agent
		// and serial logs first: deleting the instance destroys them, and they
		// are the only evidence of why a VM refused to boot. The workflow runs
		// diagnostics.sh before cleanup.sh; a local run has no such ordering, so
		// the suite keeps cleaning up after itself there.
		DeferCleanup(func(specContext SpecContext) {
			ctx, cancel := context.WithTimeout(specContext, 4*time.Minute)
			defer cancel()
			if !ownsInstance || externalCleanup {
				return
			}
			cleanup := exec.CommandContext(ctx, "bash", filepath.Join(repositoryRoot, "e2e/platform/cleanup.sh"))
			cleanup.Env = os.Environ()
			output, cleanupErr := cleanup.CombinedOutput()
			Expect(cleanupErr).NotTo(HaveOccurred(), "cleanup owned Lima instance: %s", output)
		}, NodeTimeout(5*time.Minute))
	}, NodeTimeout(2*time.Minute))

	It("installs the expected artifact and provisions a real VM instance", Label(hostStage), func(ctx SpecContext) {
		torio.setContext(ctx)
		// The installed binary must report the machine it is running on. Pinning
		// this to one host would have the assertion pass only where the archive
		// happened to be built, which is the opposite of what it is for.
		version := torio.mustRun("torio-version", "version", "version")
		expectData(version, map[string]any{
			"os":      runtime.GOOS,
			"arch":    runtime.GOARCH,
			"version": os.Getenv("PLATFORM_E2E_EXPECTED_VERSION"),
		})

		absent := torio.mustRun("vm-status-absent", "vm.status", "vm", "status")
		expectData(absent, map[string]any{"name": instanceName, "state": "not_found"})

		ownsInstance = true
		created := torio.mustRun("vm-init", "vm.init", "vm", "init", "--backend", profile.name, "--cpus", "2", "--memory", "4GiB", "--disk", "20GiB")
		expectData(created, map[string]any{"name": instanceName, "backend": profile.name, "created": true, "unchanged": false})
		// A rerun without the flag must keep the declaration rather than reset
		// it: a guest is provisioned for one agent identity, and re-declaring it
		// would leave a guest built for one being driven as another.
		unchanged := torio.mustRun("vm-init-idempotent", "vm.init", "vm", "init", "--cpus", "2", "--memory", "4GiB", "--disk", "20GiB")
		expectData(unchanged, map[string]any{"backend": profile.name, "created": false, "unchanged": true})
	}, SpecTimeout(10*time.Minute))

	It("starts a real VM", Label(guestStage), func(ctx SpecContext) {
		torio.setContext(ctx)
		requireHypervisor()

		started := torio.mustRun("vm-start", "vm.start", "vm", "start")
		expectData(started, map[string]any{"state": "running"})
		Eventually(func() (string, error) {
			status, runErr := torio.run("vm-status-eventually-running", "vm.status", "vm", "status")
			if runErr != nil {
				return "", runErr
			}
			state, ok := status.Data["state"].(string)
			if !ok {
				return "", fmt.Errorf("vm.status data.state is not a string")
			}
			return state, nil
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Equal("running"))
	}, SpecTimeout(10*time.Minute))

	It("bootstraps the backend and imports Brain content into the guest", Label(guestStage), func(ctx SpecContext) {
		torio.setContext(ctx)
		bootstrapped := torio.mustRun("vm-bootstrap", "vm.bootstrap", "vm", "bootstrap")
		expectData(bootstrapped, map[string]any{"backend": profile.name})
		torio.mustRun("vm-bootstrap-idempotent", "vm.bootstrap", "vm", "bootstrap")
		expectChecksOK(bootstrapped, profile.requiredChecks)

		versionArgs := append([]string{"vm", "ssh", "--"}, profile.versionCommand...)
		version := torio.mustRun("backend-version", "vm.ssh", versionArgs...)
		expectData(version, map[string]any{"exit_code": float64(0)})

		// The identity must not be able to become root, asserted on the live guest
		// against what bootstrap proved.
		//
		// The answer is the sentence, not the exit status. Asked by a caller that
		// already holds root, sudo 1.9.15 exits 0 whether the identity may run
		// everything or nothing — this spec asserted exit 1 and was wrong about a
		// real guest, in the same way the bootstrap check was. So the guest reads
		// its own answer and the exit code carries only that verdict here.
		noSudo := torio.mustRun("backend-no-sudo", "vm.ssh", "vm", "ssh", "--",
			"sh", "-c", "LC_ALL=C sudo -n -l -U "+profile.user+" | grep -q 'is not allowed to run sudo'")
		expectData(noSudo, map[string]any{"exit_code": float64(0)})

		backendStatus := torio.mustRun("backend-status", "backend.status", "backend", "status")
		expectData(backendStatus, map[string]any{
			"backend":           profile.name,
			"user":              profile.user,
			"registry_declared": profile.declaresRegistry,
			"service_declared":  profile.declaresService,
			"session_declared":  profile.declaresSession,
		})

		ambient := torio.mustRun("status-after-bootstrap", "status", "status")
		row := statusRowFor(ambient, instanceName)
		Expect(row["box"]).To(Equal("running"))
		backendField, ok := row["backend"].(map[string]any)
		Expect(ok).To(BeTrue(), "status backend is not an object")
		Expect(backendField).To(HaveKeyWithValue("state", "known"))
		Expect(backendField).To(HaveKeyWithValue("name", profile.name))

		if profile.declaresWaitingMarker {
			waitingField, waitingOK := row["waiting"].(map[string]any)
			Expect(waitingOK).To(BeTrue(), "status waiting is not an object")
			Expect(waitingField).To(HaveKeyWithValue("state", "known"))
			Expect(waitingField).To(HaveKeyWithValue("waiting", false))

			helper := torio.mustRun("waiting-helper-concurrent-set-clear", "vm.ssh",
				"vm", "ssh", "--", "sudo", "-u", profile.user, "-H", "--",
				"python3", "-c", fmt.Sprintf(waitingHelperExercise, profile.user))
			expectData(helper, map[string]any{"exit_code": float64(0)})

			afterHelper := torio.mustRun("status-after-waiting-helper", "status", "status")
			afterRow := statusRowFor(afterHelper, instanceName)
			afterWaiting := afterRow["waiting"].(map[string]any)
			Expect(afterWaiting).To(HaveKeyWithValue("state", "known"))
			Expect(afterWaiting).To(HaveKeyWithValue("waiting", false))
		}

		brainInit := torio.mustRun("brain-init", "brain.init", "brain", "init")
		expectData(brainInit, map[string]any{"created": true, "state": "initialized"})
		brainAgain := torio.mustRun("brain-init-idempotent", "brain.init", "brain", "init")
		expectData(brainAgain, map[string]any{"created": false, "state": "initialized"})
		brainImport := torio.mustRun("brain-import", "brain.import", "brain", "import", fixtureDir, "--into", "ci-fixture")
		expectData(brainImport, map[string]any{
			"dry_run":        false,
			"files":          float64(1),
			"markdown_files": float64(1),
			"conflicts":      float64(0),
		})
		// As hermes, not as the Lima login user: `brain init` reports the Brain
		// as 0750 hermes:hermes, so the login user cannot traverse it and `test`
		// would report the file missing when it is merely unreadable. The
		// projects tree is different — 0710 on the shared group — which is why
		// the checks there need no sudo.
		present := torio.mustRun("brain-fixture-present", "vm.ssh", "vm", "ssh", "--", "sudo", "-u", profile.user, "--", "test", "-f", profile.vault+"/ci-fixture/README.md")
		expectData(present, map[string]any{"exit_code": float64(0)})
		brainStatus := torio.mustRun("brain-status", "brain.status", "brain", "status")
		expectData(brainStatus, map[string]any{
			"state":              "initialized",
			"native_filesystem":  true,
			"path":               profile.vault,
			"project_registered": profile.declaresRegistry,
			"retrieval_skill":    "installed",
		})
		// Where the backend actually looks, read as the identity that reads it.
		// A report saying "installed" and a file the agent can open are two
		// different claims, and only the second makes the vault reachable.
		skill := torio.mustRun("brain-skill-present", "vm.ssh", "vm", "ssh", "--", "sudo", "-u", profile.user, "--", "test", "-f", profile.skillFile)
		expectData(skill, map[string]any{"exit_code": float64(0)})
	}, SpecTimeout(15*time.Minute))

	It("reports honestly about a service the backend does not declare", Label(guestStage), func(ctx SpecContext) {
		torio.setContext(ctx)
		if profile.declaresService {
			Skip("this backend declares a service; the next spec exercises it")
		}
		// A backend with no service is not an unready one. `status` answers and
		// exits 0; asking Torio to manage the service is the operator error.
		status := torio.mustRun("serve-status-undeclared", "serve.status", "serve", "status")
		expectData(status, map[string]any{"backend": profile.name, "service_declared": false})
		_, err := torio.run("serve-install-undeclared", "serve.install", "serve", "install")
		Expect(err).To(HaveOccurred(), "installing a service the backend does not declare must fail closed")
	}, SpecTimeout(5*time.Minute))

	It("installs and exercises the persistent backend service", Label(guestStage), func(ctx SpecContext) {
		torio.setContext(ctx)
		if !profile.declaresService {
			Skip("this backend declares no guest service")
		}
		installed := torio.mustRun("serve-install", "serve.install", "serve", "install")
		expectData(installed, map[string]any{"validated": true, "enabled": true})
		again := torio.mustRun("serve-install-idempotent", "serve.install", "serve", "install")
		expectData(again, map[string]any{"changed": false, "validated": true, "enabled": true})
		started := torio.mustRun("serve-start", "serve.start", "serve", "start")
		expectData(started, map[string]any{"active": true, "endpoint_ready": true, "ready": true})
		status := torio.mustRun("serve-status", "serve.status", "serve", "status")
		expectData(status, map[string]any{"active": true, "endpoint_ready": true, "ready": true})
		restarted := torio.mustRun("serve-restart", "serve.restart", "serve", "restart")
		expectData(restarted, map[string]any{"active": true, "endpoint_ready": true, "ready": true})
	}, SpecTimeout(12*time.Minute))

	It("attaches, verifies and removes a real Git project non-destructively", Label(guestStage), func(ctx SpecContext) {
		torio.setContext(ctx)
		addArgs := []string{"project", "add", "ci-fixture", fixtureRemote, "--id", "ci-fixture"}
		if profile.declaresRegistry {
			addArgs = append(addArgs, "--use")
		}
		added := torio.mustRun("project-add", "project.add", addArgs...)
		expectData(added, map[string]any{
			"id": "ci-fixture", "cloned": true, "registered": true,
			"activated": profile.declaresRegistry,
		})
		listed := torio.mustRun("project-list", "project.list", "project", "list")
		expectData(listed, map[string]any{"count": float64(1)})
		shown := torio.mustRun("project-show", "project.show", "project", "show", "ci-fixture")
		expectData(shown, map[string]any{
			"checkout.path_exists":          true,
			"checkout.repository":           true,
			"checkout.origin_matches":       true,
			"checkout.full_clone":           true,
			"checkout.no_credential_helper": true,
			"checkout.shared_permissions":   true,
			"issues":                        []any{},
		})

		checkout := torio.mustRun("project-fixture-checkout", "vm.ssh", "vm", "ssh", "--", "sudo", "-u", profile.user, "--", "git", "-C", profile.workspace+"/ci-fixture", "checkout", "--detach", fixtureCommit)
		expectData(checkout, map[string]any{"exit_code": float64(0)})
		head := torio.mustRun("project-fixture-head", "vm.ssh", "vm", "ssh", "--", "sudo", "-u", profile.user, "--", "git", "-C", profile.workspace+"/ci-fixture", "rev-parse", "HEAD")
		expectData(head, map[string]any{"exit_code": float64(0), "stdout": fixtureCommit + "\n"})
		if profile.declaresRegistry {
			used := torio.mustRun("project-use", "project.use", "project", "use", "ci-fixture")
			expectData(used, map[string]any{"active": true})
		} else {
			// A backend with no registry has no active project to set, and says
			// so rather than pretending the call succeeded.
			_, err := torio.run("project-use-undeclared", "project.use", "project", "use", "ci-fixture")
			Expect(err).To(HaveOccurred(), "activating on a backend with no registry must fail closed")
		}
		removed := torio.mustRun("project-remove", "project.remove", "project", "remove", "ci-fixture")
		expectData(removed, map[string]any{"checkout_retained": true})
		empty := torio.mustRun("project-list-empty", "project.list", "project", "list")
		expectData(empty, map[string]any{"count": float64(0)})
		retained := torio.mustRun("project-checkout-retained", "vm.ssh", "vm", "ssh", "--", "test", "-d", profile.workspace+"/ci-fixture")
		expectData(retained, map[string]any{"exit_code": float64(0)})
	}, SpecTimeout(12*time.Minute))

	It("stops services and the VM idempotently", Label(guestStage), func(ctx SpecContext) {
		torio.setContext(ctx)
		if profile.declaresService {
			stoppedService := torio.mustRun("serve-stop", "serve.stop", "serve", "stop")
			expectData(stoppedService, map[string]any{"active": false})
			stoppedAgain := torio.mustRun("serve-stop-idempotent", "serve.stop", "serve", "stop")
			expectData(stoppedAgain, map[string]any{"active": false})
		} else {
			// Stopping a service the backend never declared is the same operator
			// error as installing one, and refusing it is the contract holding —
			// not something to route around by asking for it anyway.
			_, err := torio.run("serve-stop-undeclared", "serve.stop", "serve", "stop")
			Expect(err).To(HaveOccurred(), "stopping a service the backend does not declare must fail closed")
		}
		stoppedVM := torio.mustRun("vm-stop", "vm.stop", "vm", "stop")
		expectData(stoppedVM, map[string]any{"state": "stopped"})
		stoppedVMAgain := torio.mustRun("vm-stop-idempotent", "vm.stop", "vm", "stop")
		expectData(stoppedVMAgain, map[string]any{"state": "stopped"})
		status := torio.mustRun("vm-status-stopped", "vm.status", "vm", "status")
		expectData(status, map[string]any{"state": "stopped"})
	}, SpecTimeout(8*time.Minute))
})

// statusRowFor returns the `torio status` row for one instance, and fails
// naming what it did see when there is none.
//
// It finds the row rather than taking the first, and asserts nothing about how
// many there are. `torio status` polls every box on the machine, so a run on a
// developer's laptop sees their own boxes beside the one this journey built. The
// spec's subject is the box it just created; requiring it to be the only one
// made the journey pass on a clean CI runner and fail on every machine that had
// ever used the product, which is the wrong way round for a gate whose job is to
// catch what CI cannot.
func statusRowFor(rep envelope, instanceName string) map[string]any {
	GinkgoHelper()
	instances, ok := rep.Data["instances"].([]any)
	Expect(ok).To(BeTrue(), "status data carries no instances array")

	seen := make([]string, 0, len(instances))
	for _, raw := range instances {
		row, isMap := raw.(map[string]any)
		Expect(isMap).To(BeTrue(), "status instance is not an object")
		name, _ := row["instance"].(string)
		seen = append(seen, name)
		if name == instanceName {
			return row
		}
	}
	Fail(fmt.Sprintf("status reports no row for %q; it listed %v", instanceName, seen))
	return nil
}

func environmentOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func resolveRepositoryRoot() string {
	GinkgoHelper()
	_, source, _, ok := runtime.Caller(0)
	Expect(ok).To(BeTrue(), "resolve platform E2E source path")
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func limaInstanceExists(instanceName string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "limactl", "list", "--quiet")
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return false, err
	}
	for _, name := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if name == instanceName {
			return true, nil
		}
	}
	return false, nil
}

func supportedHost() bool {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64", "linux/amd64":
		return true
	default:
		return false
	}
}

// requireHypervisor fails the guest stage before Lima is asked to boot anything.
// Without it the failure surfaces as `limactl start` exiting after the host
// agent quits with an empty error list, which says nothing about the cause.
func requireHypervisor() {
	GinkgoHelper()
	switch runtime.GOOS {
	case "darwin":
		requireAppleVirtualization()
	case "linux":
		requireKVM()
	default:
		Fail(fmt.Sprintf("no hypervisor check for %s; the guest stage supports darwin and linux", runtime.GOOS))
	}
}

func requireAppleVirtualization() {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// A host that cannot virtualize reports 0 — and a host that is itself a
	// guest does not publish the OID at all, which is what GitHub's macOS arm64
	// runners do. Both mean the same thing here, so neither is an error to
	// propagate: they are the answer.
	output, err := exec.CommandContext(ctx, "sysctl", "-n", "kern.hv_support").CombinedOutput()
	observed := strings.TrimSpace(string(output))
	if err != nil {
		observed = fmt.Sprintf("unreadable (%v: %s)", err, observed)
	}
	Expect(observed).To(Equal("1"),
		"kern.hv_support is %s: this host cannot use Apple's Virtualization framework, "+
			"so Lima's vz driver cannot boot a guest. GitHub-hosted macOS arm64 runners are "+
			"themselves VMs without nested virtualization; run the guest stage on real "+
			"Apple Silicon.", observed)
}

// requireKVM checks what actually stops the qemu driver: a /dev/kvm this
// process may open. Presence alone is not enough — hosted runners deliver the
// node unwritable, and without the accompanying udev rule qemu silently falls
// back to TCG emulation, which does not fail so much as never finish.
func requireKVM() {
	GinkgoHelper()
	info, err := os.Stat("/dev/kvm")
	Expect(err).ToNot(HaveOccurred(),
		"/dev/kvm is absent: this host cannot use KVM, so Lima's qemu driver would "+
			"fall back to emulation. Run the guest stage on a Linux host with nested "+
			"virtualization enabled.")
	Expect(info.Mode()&os.ModeDevice).ToNot(BeZero(), "/dev/kvm is not a device node")

	node, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	Expect(err).ToNot(HaveOccurred(),
		"/dev/kvm is present but not writable by this user. Hosted runners deliver it "+
			"that way; the workflow's Enable KVM step installs the udev rule that fixes it.")
	Expect(node.Close()).To(Succeed())
}

func captureTechnicalVersions(artifactDir string) {
	GinkgoHelper()
	type versionCommand struct {
		label string
		name  string
		args  []string
		// A host that is itself a guest does not publish kern.hv_support at all,
		// so sysctl exits non-zero. That output is the diagnostic — recording it
		// is the point, and failing on it would throw the evidence away.
		recordFailure bool
	}
	commands := []versionCommand{
		{label: "limactl-version.txt", name: "limactl", args: []string{"--version"}},
		{label: "go-version.txt", name: "go", args: []string{"version"}},
	}
	// The hypervisor evidence is whatever the host can be asked about. Running
	// the macOS probes on Linux would fail the capture on a healthy machine and
	// throw away the diagnostics the failure was collected for.
	switch runtime.GOOS {
	case "darwin":
		commands = append(commands,
			versionCommand{label: "macos-version.txt", name: "sw_vers"},
			versionCommand{label: "hv-support.txt", name: "sysctl", args: []string{"kern.hv_support"}, recordFailure: true},
		)
	case "linux":
		commands = append(commands,
			versionCommand{label: "kernel-version.txt", name: "uname", args: []string{"-a"}},
			versionCommand{label: "kvm-node.txt", name: "ls", args: []string{"-l", "/dev/kvm"}, recordFailure: true},
			versionCommand{label: "qemu-version.txt", name: "qemu-system-x86_64", args: []string{"--version"}, recordFailure: true},
		)
	}
	for _, command := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, command.name, command.args...)
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		err := cmd.Run()
		cancel()
		if !command.recordFailure {
			Expect(err).NotTo(HaveOccurred(), "capture %s: %s", command.label, output.String())
		}
		Expect(writeArtifact(filepath.Join(artifactDir, command.label), output.Bytes())).To(Succeed())
	}
}
