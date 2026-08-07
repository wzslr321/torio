#!/usr/bin/env python3
"""Structural contract for the real Apple Silicon/Lima E2E gate."""

from __future__ import annotations

import os
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / ".github" / "workflows" / "platform-e2e.yml"
RELEASE_WORKFLOW = ROOT / ".github" / "workflows" / "release.yml"
JOURNEY = ROOT / "e2e" / "platform" / "journey_test.go"
DRIVER = ROOT / "e2e" / "platform" / "driver_test.go"
ENVELOPE = ROOT / "e2e" / "platform" / "envelope.go"
CLEANUP = ROOT / "e2e" / "platform" / "cleanup.sh"
DIAGNOSTICS = ROOT / "e2e" / "platform" / "diagnostics.sh"
WITH_TIMEOUT = ROOT / "e2e" / "platform" / "with_timeout.py"
MAKEFILE = ROOT / "Makefile"


class PlatformE2EContractTests(unittest.TestCase):
    def test_workflow_gates_on_a_real_linux_guest_by_default(self) -> None:
        # Linux is the default because it is the only hosted runner that can
        # boot a guest at all: macOS runners are themselves VMs and report
        # kern.hv_support = 0. Defaulting back to macOS would silently return
        # the gate to its host half and prove nothing about a running VM.
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("runs-on: ${{ inputs.runner || 'ubuntu-24.04' }}", text)
        self.assertIn("workflow_call:", text)
        self.assertIn("permissions:\n  contents: read", text)
        self.assertIn("timeout-minutes: ${{ (inputs.stage || 'full') == 'full' && 45 || 25 }}", text)
        self.assertIn("timeout-minutes: ${{ (inputs.stage || 'full') == 'full' && 30 || 15 }}", text)
        for step, timeout in (
            ("Checkout", 3),
            ("Set up Go", 5),
            ("Install verified Lima 2.2.0", 5),
            ("Build and install release-shaped Torio", 10),
        ):
            self.assertRegex(
                text,
                rf"- name: {re.escape(step)}\n\s+timeout-minutes: {timeout}\n",
            )
        self.assertIn(
            "- name: Upload Ginkgo JUnit report\n"
            "        if: always()\n"
            "        timeout-minutes: 2\n",
            text,
        )
        self.assertIn(
            "- name: Upload failure diagnostics\n"
            "        if: failure() || cancelled()\n"
            "        timeout-minutes: 2\n",
            text,
        )
        self.assertIn("TORIO_INSTANCE: torio-ci-${{ github.run_id }}-${{ github.run_attempt }}", text)
        self.assertIn("make package-release VERSION=0.0.0", text)
        self.assertIn("scripts/install.sh", text)
        self.assertIn("PLATFORM_E2E_TORIO_BIN", text)
        self.assertIn("PLATFORM_E2E_EXPECTED_VERSION: 0.0.0", text)
        self.assertIn("make platform-e2e", text)
        self.assertIn("if: failure() || cancelled()", text)
        self.assertIn("bash e2e/platform/diagnostics.sh", text)
        self.assertIn("if: always()", text)
        self.assertIn("bash e2e/platform/cleanup.sh", text)
        self.assertIn("ginkgo-junit.xml", text)
        self.assertIn("platform-e2e-junit-${{ github.run_id }}-${{ github.run_attempt }}", text)
        self.assertNotIn("continue-on-error", text)
        self.assertNotIn("limactl fake", text.lower())

    def test_workflow_selects_the_stage_and_rejects_an_unknown_one(self) -> None:
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("PLATFORM_E2E_STAGE: ${{ inputs.stage || 'full' }}", text)
        self.assertIn("host) export PLATFORM_E2E_LABEL_FILTER='!guest' ;;", text)
        self.assertIn("full) export PLATFORM_E2E_LABEL_FILTER='' ;;", text)
        self.assertRegex(text, r"\*\) printf 'unknown stage %s\\n'.*exit 2")
        self.assertIn("options: [host, full]", text)

    def test_workflow_makes_dev_kvm_usable_before_asking_lima_to_boot(self) -> None:
        # /dev/kvm is present on hosted Linux runners but not writable as
        # delivered. Without the rule, qemu falls back
        # to TCG emulation, which does not fail so much as never finish -- the
        # job would burn its timeout and report nothing useful.
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("- name: Enable KVM", text)
        self.assertIn("if: runner.os == 'Linux'", text)
        self.assertIn('KERNEL=="kvm", GROUP="kvm", MODE="0666"', text)
        self.assertIn("test -w /dev/kvm", text)
        # `udevadm trigger` returns before the rule has been applied, so reading
        # writability straight after it is a race -- one a first run won and a
        # second lost. The settle and the direct chmod are what make the step
        # true when it exits rather than usually true.
        self.assertIn("udevadm settle", text)
        self.assertIn("sudo chmod 0666 /dev/kvm", text)
        self.assertIn("qemu-system-x86", text)
        self.assertLess(
            text.index("- name: Enable KVM"),
            text.index("- name: Run real product journey"),
            "KVM must be usable before the journey boots anything",
        )

    def test_workflow_verifies_a_lima_archive_per_runner_family(self) -> None:
        # One pinned checksum cannot cover two runner families, and an
        # unverified download is the one step that would let a compromised
        # release binary run as the thing under test.
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("lima-2.2.0-Linux-x86_64.tar.gz", text)
        self.assertIn("a0ea1ccf6b7335a900adb5f8d2b8384457965fecb1ba72f09b4e3e46d12f424a", text)
        self.assertIn("lima-2.2.0-Darwin-arm64.tar.gz", text)
        self.assertIn("bbdef91774885a0d05f7b048c4eb89ae2bcf3a0c252ae7ca7934e63df76d93c3", text)
        self.assertIn("sha256sum -c -", text)
        self.assertIn("shasum -a 256 -c -", text)
        self.assertRegex(text, r"\*\) echo \"unsupported runner OS \$\{RUNNER_OS\}\" >&2; exit 1 ;;")
        self.assertIn("retention-days: 7", text)

    def test_workflow_collects_diagnostics_before_anything_deletes_the_instance(self) -> None:
        # `limactl delete` destroys ha.stderr.log and the serial console log, the
        # only evidence of why a VM refused to boot. The suite therefore hands
        # teardown to the workflow, which reads before it removes.
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn('PLATFORM_E2E_EXTERNAL_CLEANUP: "1"', text)
        self.assertLess(
            text.index("bash e2e/platform/diagnostics.sh"),
            text.index("bash e2e/platform/cleanup.sh"),
            "diagnostics must be collected before the instance is removed",
        )
        journey = JOURNEY.read_text(encoding="utf-8")
        self.assertIn('os.Getenv("PLATFORM_E2E_EXTERNAL_CLEANUP") == "1"', journey)
        self.assertIn("if !ownsInstance || externalCleanup {", journey)

    def test_journey_splits_at_the_hypervisor_boundary(self) -> None:
        # Hosted macOS arm64 runners are VMs without nested virtualization, so
        # they can never boot a Lima guest. Everything up to `vm init` still runs
        # there and is what gates PRs and releases.
        journey = JOURNEY.read_text(encoding="utf-8")
        self.assertIn('hostStage  = "host"', journey)
        self.assertIn('guestStage = "guest"', journey)
        self.assertRegex(
            journey,
            r'It\("installs the expected artifact and provisions a real VM instance", Label\(hostStage\)',
        )
        self.assertRegex(journey, r'It\("starts a real VM", Label\(guestStage\)')
        for spec in (
            "bootstraps the backend and imports Brain content into the guest",
            "reports honestly about a service the backend does not declare",
            "installs and exercises the persistent backend service",
            "attaches, verifies and removes a real Git project non-destructively",
            "stops services and the VM idempotently",
        ):
            with self.subTest(spec=spec):
                self.assertIn(f'It("{spec}", Label(guestStage)', journey)
        self.assertIn("requireHypervisor()", journey)
        self.assertIn('"sysctl", "-n", "kern.hv_support"', journey)
        self.assertIn('{label: "hv-support.txt"', journey)

    def test_concurrency_group_separates_the_two_release_gates(self) -> None:
        # The release calls this workflow twice in one run: the Linux guest gate
        # and the darwin host gate. In a reusable workflow the `github` context
        # is the caller's, so `github.workflow` and `github.ref` are the same
        # string for both calls -- a group built from those alone would put the
        # two gates in one concurrency group with cancel-in-progress, and the
        # second to start would cancel the first. `package` needs both, so the
        # tag would produce no assets at all.
        text = WORKFLOW.read_text(encoding="utf-8")
        group = next(
            line for line in text.splitlines() if line.strip().startswith("group:")
        )
        self.assertIn("inputs.runner", group)
        self.assertIn("inputs.stage", group)
        self.assertIn("cancel-in-progress: true", text)

    def test_release_waits_for_the_same_real_platform_gate(self) -> None:
        text = RELEASE_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("uses: ./.github/workflows/platform-e2e.yml", text)
        self.assertIn("checkout_ref: ${{ inputs.tag || github.ref }}", text)
        self.assertIn("ref: ${{ inputs.tag || github.ref }}", text)
        self.assertIn("stage: host", text)
        self.assertRegex(
            text,
            re.compile(r"package:\n(?:.*\n)*?\s+needs: \[linux-e2e, darwin-e2e\]", re.MULTILINE),
        )

    def test_ginkgo_journey_drives_the_real_vertical_product_slice(self) -> None:
        text = JOURNEY.read_text(encoding="utf-8")
        required = [
            # The host gate is a matrix check, not a single-platform equality.
            # Pinning it back to darwin would silently reintroduce a suite that
            # can only ever run where the guest stage cannot.
            "Expect(supportedHost()).To(BeTrue()",
            '"darwin/arm64", "linux/amd64"',
            'exec.LookPath("limactl")',
            '"vm", "init"',
            '"vm", "start"',
            '"vm", "bootstrap"',
            '"brain", "init"',
            '"brain", "import"',
            "brain-fixture-present",
            '"serve", "install"',
            '"serve", "start"',
            '"serve", "status"',
            '"project", "add"',
            '"project", "show"',
            '"project", "remove"',
            '"vm", "stop"',
        ]
        for command in required:
            with self.subTest(command=command):
                self.assertIn(command, text)
        self.assertIn('os.Getenv("TORIO_INSTANCE")', text)
        self.assertIn('torio.mustRun("torio-version", "version", "version")', text)
        self.assertIn("PLATFORM_E2E_EXPECTED_VERSION", text)
        self.assertIn("BeforeAll", text)
        self.assertIn("DeferCleanup", text)
        self.assertIn("ownsInstance = true", text)
        self.assertIn('filepath.Join(repositoryRoot, "e2e/platform/cleanup.sh")', text)
        self.assertNotRegex(text, r"fake|stub|mock")

    def test_project_fixture_is_public_and_checks_a_known_commit(self) -> None:
        text = JOURNEY.read_text(encoding="utf-8")
        self.assertIn("https://github.com/octocat/Hello-World.git", text)
        self.assertIn("7fd1a60b01f91b314f59955a4e4d4e80d8edf11d", text)
        self.assertIn('"checkout", "--detach", fixtureCommit', text)
        self.assertIn('"rev-parse", "HEAD"', text)
        self.assertNotIn("https://github.com/wzslr321/torio.git", text)

    def test_make_target_is_phony_and_invokes_the_ginkgo_suite(self) -> None:
        text = MAKEFILE.read_text(encoding="utf-8")
        phony = next(line for line in text.splitlines() if line.startswith(".PHONY:"))
        self.assertIn("platform-e2e", phony.split())
        self.assertRegex(text, r"(?m)^platform-e2e:$")
        # -C e2e is load-bearing: the suites are their own module, so a target
        # that dropped it would run against the root module and fail to resolve
        # Ginkgo at all.
        self.assertIn("go test -C e2e -count=1 -tags=platform_e2e", text)
        self.assertIn('-ginkgo.label-filter="$$PLATFORM_E2E_LABEL_FILTER"', text)
        self.assertIn("-ginkgo.junit-report=", text)

    def test_go_driver_strictly_validates_json_and_records_artifacts(self) -> None:
        driver = DRIVER.read_text(encoding="utf-8")
        envelope = ENVELOPE.read_text(encoding="utf-8")
        self.assertIn("DisallowUnknownFields", envelope)
        self.assertIn('got.SchemaVersion != "1"', envelope)
        self.assertIn("len(warnings) != 0", envelope)
        self.assertIn('string(got.Error) != "null"', envelope)
        self.assertIn("expected one JSON document", envelope)
        self.assertIn("writeArtifact", driver)

    def test_outer_cleanup_is_exactly_scoped_to_the_ci_instance(self) -> None:
        text = CLEANUP.read_text(encoding="utf-8")
        self.assertIn('TORIO_INSTANCE="${TORIO_INSTANCE:-torio-ci-local}"', text)
        self.assertIn('limactl list --quiet', text)
        self.assertIn('[[ "${name}" == "${TORIO_INSTANCE}" ]]', text)
        self.assertIn('limactl delete --force --tty=false "${TORIO_INSTANCE}"', text)
        self.assertIn('for attempt in 1 2 3', text)
        self.assertIn('with_timeout.py', text)
        self.assertIn('"${TIMEOUT}" 40', text)

    def test_outer_diagnostics_never_dump_brain_or_project_content(self) -> None:
        text = DIAGNOSTICS.read_text(encoding="utf-8")
        self.assertIn('limactl list --json', text)
        self.assertIn('journalctl -u cloud-final', text)
        self.assertIn('journalctl --user -u hermes-serve.service', text)
        self.assertNotIn('/home/hermes/brain', text)
        self.assertNotIn('/home/hermes/projects', text)

    def test_outer_diagnostics_record_the_hostagent_socket_path(self) -> None:
        # ha.sock is a socket, so a non-recursive cp of it can never succeed;
        # the artifact records the path itself.
        text = DIAGNOSTICS.read_text(encoding="utf-8")
        self.assertIn(
            'printf \'%s\\n\' "${instance_dir}/ha.sock" '
            '> "${ARTIFACT_DIR}/hostagent-socket-path.txt"',
            text,
        )
        self.assertNotIn('cp -f "${instance_dir}/ha.sock"', text)

    def test_timeout_helper_returns_124_without_using_a_shell(self) -> None:
        proc = subprocess.run(
            [
                sys.executable,
                str(WITH_TIMEOUT),
                "0.05",
                sys.executable,
                "-c",
                "import time; time.sleep(2)",
            ],
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(proc.returncode, 124, proc.stderr)
        self.assertNotIn("shell=True", WITH_TIMEOUT.read_text(encoding="utf-8"))

    def test_timeout_helper_terminates_the_subprocess_group(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            marker = Path(tmp) / "descendant-survived"
            child = (
                "import pathlib,signal,time; "
                "signal.signal(signal.SIGTERM, signal.SIG_IGN); time.sleep(0.4); "
                f"pathlib.Path({str(marker)!r}).write_text('bad')"
            )
            parent = (
                "import subprocess,sys,time; "
                f"subprocess.Popen([sys.executable, '-c', {child!r}]); "
                "time.sleep(2)"
            )
            proc = subprocess.run(
                [sys.executable, str(WITH_TIMEOUT), "0.05", sys.executable, "-c", parent],
                env={**os.environ},
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertEqual(proc.returncode, 124, proc.stderr)
            import time

            time.sleep(0.5)
            self.assertFalse(marker.exists(), "a timed-out descendant survived")

    def test_timeout_helper_cleans_up_when_it_receives_term(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            marker = Path(tmp) / "descendant-survived-term"
            child = (
                "import pathlib,signal,time; "
                "signal.signal(signal.SIGTERM, signal.SIG_IGN); time.sleep(0.4); "
                f"pathlib.Path({str(marker)!r}).write_text('bad')"
            )
            process = subprocess.Popen(
                [sys.executable, str(WITH_TIMEOUT), "10", sys.executable, "-c", child],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            import time

            time.sleep(0.1)
            process.terminate()
            process.communicate(timeout=3)
            time.sleep(0.5)
            self.assertFalse(marker.exists(), "a descendant survived SIGTERM")

if __name__ == "__main__":
    unittest.main()
