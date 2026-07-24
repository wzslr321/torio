#!/usr/bin/env python3
"""Regression contracts for the S2 lifecycle driver and negative harness.

These tests are host-only: they intentionally do not require Lima or systemd. The
live macOS/Lima replay remains a separate evidence step.
"""

from __future__ import annotations

import os
from pathlib import Path
import re
import stat
import subprocess
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[1]
DRIVER = ROOT / "spikes" / "s2_gateway_lifecycle.sh"
HARNESS = ROOT / "spikes" / "s2_negative_harness.sh"


def function_block(text: str, name: str) -> str:
    """Return one top-level shell function, ignoring mentions in comments."""
    match = re.search(rf"(?m)^{re.escape(name)}\(\) \{{\n", text)
    if match is None:
        raise AssertionError(f"function {name} not found")
    end = text.find("\n}\n", match.end())
    if end < 0:
        raise AssertionError(f"function {name} has no closing brace")
    return text[match.start() : end + 3]


class S2DriverContracts(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.driver = DRIVER.read_text()
        cls.harness = HARNESS.read_text()

    def test_run_id_is_validated_before_remote_path_is_built(self) -> None:
        validate = self.driver.index('validate_run_id "$RUN_ID"')
        home = self.driver.index('S2_HOME="/home/hermes/.hermes-s2-spike-${RUN_ID}"')
        self.assertLess(validate, home)

    def test_linger_probe_uses_lstat_and_requires_zero_transport_exit(self) -> None:
        probe = function_block(self.driver, "probe_linger")
        self.assertIn("os.lstat", probe)
        self.assertNotIn("os.path.exists", probe)
        self.assertIn('LINGER_STATE=error', probe)
        self.assertIn('[ "$ec" -eq 0 ]', probe)

    def test_unit_ownership_is_armed_before_install(self) -> None:
        arm = self.driver.index("UNIT_MUTATION_STARTED=1")
        install = self.driver.index('hx "hermes gateway install')
        self.assertLess(arm, install)
        cleanup = function_block(self.driver, "cleanup")
        self.assertIn('UNIT_MUTATION_STARTED', cleanup)

    def test_unit_identity_is_captured_before_ownership_and_gates_mutation(self) -> None:
        capture = function_block(self.driver, "capture_owned_unit_identity")
        probe = function_block(self.driver, "probe_owned_unit")
        install = self.driver.index('hx "hermes gateway install')
        owned = self.driver.index("OWNED_UNIT=1", install)
        capture_call = self.driver.index("capture_owned_unit_identity", install)
        self.assertLess(install, capture_call)
        self.assertLess(capture_call, owned)
        for token in (
            "os.lstat",
            "stat.S_ISREG",
            "st_dev",
            "st_ino",
            "hashlib.sha256",
            "os.O_NOFOLLOW",
            "os.fstat",
            "HERMES_HOME=",
            "FragmentPath",
        ):
            self.assertIn(token, capture)
        self.assertIn('print("UNIT_OWNER=" + state', probe)
        for state in ("MATCH", "ABSENT", "MISMATCH", "ERROR"):
            self.assertIn(f'result("{state}"', probe)
        self.assertIn("UNIT_DEV", self.driver)
        self.assertIn("UNIT_INO", self.driver)
        self.assertIn("UNIT_SHA256", self.driver)
        self.assertIn("UNIT_ID_CAPTURED", self.driver)
        stop = function_block(self.driver, "stop_owned_gateway_verified")
        uninstall = function_block(self.driver, "uninstall_owned_gateway_verified")
        for block in (stop, uninstall):
            self.assertIn("probe_owned_unit", block)
            self.assertIn('"$UNIT_OWNER_STATE" != "MATCH"', block)
        self.assertEqual(self.driver.count('hx "hermes gateway stop"'), 1)
        self.assertEqual(self.driver.count('hx "hermes gateway uninstall"'), 1)
        self.assertGreaterEqual(self.driver.count("stop_owned_gateway_verified"), 4)
        self.assertGreaterEqual(self.driver.count("uninstall_owned_gateway_verified"), 3)

    def test_setup_findings_abort_before_start(self) -> None:
        install = self.driver.index('hx "hermes gateway install')
        start = self.driver.index('hx "hermes gateway start')
        between = self.driver[install:start]
        self.assertIn("abort_setup_if_findings", between)

    def test_normal_uninstall_is_guarded_by_teardown_prerequisites(self) -> None:
        marker = self.driver.index('banner "O — teardown')
        uninstall = self.driver.index('uninstall_owned_gateway_verified "final-teardown"', marker)
        self.assertLess(marker, uninstall)
        self.assertIn('if teardown_prereqs_verified "before-uninstall"; then', self.driver[marker:uninstall])

    def test_final_absence_proves_system_load_state(self) -> None:
        proof = function_block(self.driver, "prove_gateway_absent")
        self.assertIn("SYSTEM_LOADSTATE", proof)
        self.assertIn('["systemctl", "show"', proof)

    def test_socket_probe_requires_stable_service_cgroup_and_process_identity(self) -> None:
        socket = self.driver.split("cat <<'RSOCK'", 1)[1].split("RSOCK", 1)[0]
        for token in (
            "InvocationID",
            "MainPID",
            "ControlGroup",
            "st_dev",
            "st_ino",
            '"/proc/%d/stat"',
            "starttime",
            "SOCKET_STABLE=1",
        ):
            self.assertIn(token, socket)
        self.assertGreaterEqual(socket.count("cgroup.procs"), 2)
        self.assertIn('SOCKET_STABLE=0', self.driver)
        self.assertIn('if [ "$SOCKET_STABLE" -ne 1 ]; then', self.driver)

    def test_journal_requires_valid_invocation_id(self) -> None:
        self.assertIn("INVOCATION_ID_OK=1", self.driver)
        self.assertIn('if [ "$INVOCATION_ID_OK" -ne 1 ]; then', self.driver)

    def test_no_dispatch_parses_provider_platforms_profiles_and_real_worker_shape(self) -> None:
        assertion = function_block(self.driver, "assert_no_dispatch_state")
        for required in (
            "Provider:",
            "Messaging Platforms",
            "list_profiles",
            "get_active_profile_name",
            "HERMES_KANBAN_TASK",
            "work kanban task",
        ):
            self.assertIn(required, assertion)
        self.assertNotIn("PROFILE_ROWS =", assertion)

    def test_active_foreign_reaches_scanner_with_linger_disabled(self) -> None:
        start = self.harness.index("active-foreign)")
        end = self.harness.index("dangling-unit-link)", start)
        scenario = self.harness[start:end]
        disable = scenario.index('disable_owned_linger_verified "$ACTIVE_LINGER_ID"')
        run = scenario.index("run_driver 3")
        self.assertLess(disable, run)
        self.assertIn('expect_eq "active-foreign seeded linger baseline"', scenario[:run])
        self.assertIn('"disabled"', scenario[:run])

    def test_foreign_teardown_uses_seed_ledgers_and_fresh_identity_gates(self) -> None:
        active = self.harness[
            self.harness.index("active-foreign)") : self.harness.index("dangling-unit-link)")
        ]
        global_foreign = self.harness[
            self.harness.index("global-foreign)") : self.harness.index("transient-foreign)")
        ]
        transient = self.harness[
            self.harness.index("transient-foreign)") : self.harness.index("console-process-foreign)")
        ]
        stop = function_block(self.harness, "stop_owned_foreign_service_verified")
        for token in ("InvocationID", "MainPID", "starttime", "ControlGroup", "FragmentPath"):
            self.assertIn(token, stop)
        self.assertIn('got != os.environ["EXPECTED_SERVICE_ID"]', stop)
        self.assertLess(
            stop.index('got != os.environ["EXPECTED_SERVICE_ID"]'),
            stop.index('["systemctl", "--user", "stop"'),
        )
        for scenario, prefix in (
            (active, "ACTIVE"),
            (global_foreign, "GLOBAL"),
            (transient, "TRANSIENT"),
        ):
            self.assertIn(f'{prefix}_SERVICE_PRE="$(service_identity_snapshot)"', scenario)
            self.assertIn(f'{prefix}_SERVICE_POST="$(service_identity_snapshot)"', scenario)
            self.assertIn(f'stop_owned_foreign_service_verified "${prefix}_SERVICE_PRE"', scenario)
        self.assertNotIn("disable --now", active + global_foreign + transient)
        self.assertNotIn("reset-failed", active + global_foreign + transient)
        self.assertNotIn("rm -f", active + global_foreign + transient)

    def test_file_backed_foreign_teardown_is_fd_safe_exact_unlink(self) -> None:
        capture = function_block(self.harness, "unit_file_identity_snapshot")
        unlink = function_block(self.harness, "unlink_owned_unit_file_verified")
        for token in ("os.lstat", "st_dev", "st_ino", "hashlib.sha256", "os.O_NOFOLLOW", "os.fstat"):
            self.assertIn(token, capture)
            self.assertIn(token, unlink)
        self.assertIn("os.unlink(name, dir_fd=pfd)", unlink)
        self.assertIn("pre-unlink-identity", unlink)
        for begin, end, prefix, owner in (
            ("active-foreign)", "dangling-unit-link)", "ACTIVE", "hermes"),
            ("global-foreign)", "transient-foreign)", "GLOBAL", "root"),
        ):
            scenario = self.harness[self.harness.index(begin) : self.harness.index(end)]
            self.assertIn(f'{prefix}_UNIT_ID="$(unit_file_identity_snapshot', scenario)
            self.assertIn(f'{prefix}_UNIT_POST="$(unit_file_identity_snapshot', scenario)
            self.assertIn(f'unlink_owned_unit_file_verified "${prefix}_UNIT_PATH" {owner} "${prefix}_UNIT_ID"', scenario)
            self.assertIn("os.O_EXCL", scenario)
            self.assertIn("os.O_NOFOLLOW", scenario)

    def test_fixture_owned_linger_marker_is_pinned_before_verified_disable(self) -> None:
        marker = function_block(self.harness, "linger_marker_identity_snapshot")
        disable = function_block(self.harness, "disable_owned_linger_verified")
        self.assertIn("os.lstat", marker)
        self.assertIn("st_dev", marker)
        self.assertIn("st_ino", marker)
        self.assertIn("got != want", disable)
        self.assertLess(disable.index("got != want"), disable.index('"disable-linger", "hermes"'))
        active = self.harness[
            self.harness.index("active-foreign)") : self.harness.index("dangling-unit-link)")
        ]
        run = active.index("run_driver 3")
        self.assertEqual(active.count("disable_owned_linger_verified"), 1)
        self.assertLess(active.index("disable_owned_linger_verified"), run)
        self.assertNotIn("disable_owned_linger_verified", active[run:])

    def test_harness_uses_owned_fixture_not_wildcard_reset(self) -> None:
        self.assertNotIn(".hermes-s2-spike-*", self.harness)
        self.assertIn("HARNESS_RUN_ID=", self.harness)
        self.assertIn("HARNESS_HOME=", self.harness)
        self.assertIn('"S2_RUN_ID=${HARNESS_RUN_ID}"', self.harness)
        self.assertIn("require_clean_baseline", self.harness)
        self.assertIn("repair_owned_fixture", self.harness)
        self.assertNotIn("reset_clean", self.harness)

    def test_foreign_fixture_matrix_covers_dangling_permission_global_transient_console(self) -> None:
        for scenario in (
            "dangling-unit-link",
            "unreadable-search-path",
            "global-foreign",
            "transient-foreign",
            "console-process-foreign",
        ):
            self.assertIn(scenario, self.harness)
        self.assertIn("/run/systemd/user", self.harness)
        self.assertIn("os.symlink", self.harness)
        self.assertIn("/etc/systemd/user/hermes-gateway.service", self.harness)
        self.assertIn("systemd-run --user --unit hermes-gateway.service", self.harness)
        self.assertIn("exec -a hermes", self.harness)
        self.assertIn("gateway run", self.harness)
        self.assertIn("assert_foreign_active", self.harness)
        self.assertIn("service_identity_snapshot", self.harness)
        snapshot = function_block(self.harness, "service_identity_snapshot")
        self.assertIn('a["ActiveState"] != "active"', snapshot)
        self.assertIn('a["SubState"] != "running"', snapshot)
        self.assertIn("MainPID", snapshot)

    def test_new_foreign_fixtures_are_identity_gated_and_capture_failures_abort(self) -> None:
        dangling = self.harness[
            self.harness.index("dangling-unit-link)") : self.harness.index("unreadable-search-path)")
        ]
        unreadable = self.harness[
            self.harness.index("unreadable-search-path)") : self.harness.index("global-foreign)")
        ]
        self.assertIn("HARNESS_RUN_ID", dangling)
        self.assertIn("readlink", dangling)
        self.assertIn("exit 1", dangling)
        self.assertIn("UPATH_CANDIDATES", unreadable)
        self.assertIn("UDEV", unreadable)
        self.assertIn("UINO", unreadable)
        self.assertIn("exit 1", unreadable)
        self.assertNotIn('UPATH="/run/systemd/user"', unreadable)

    def test_final_vm_stop_is_armed_before_mutation(self) -> None:
        final = self.driver.split('banner "O — restore VM run-state', 1)[1]
        marker = final.index("VM_REBOOTING=1")
        kind = final.index('VM_TRANSITION_KIND="final-stop"')
        stop = final.index('host limactl stop "$VM"')
        verify = final.index('[ "$FINAL_VM" = "Stopped" ]')
        clear = final.index("VM_REBOOTING=0", verify)
        self.assertLess(kind, stop)
        self.assertLess(marker, stop)
        self.assertLess(stop, verify)
        self.assertLess(verify, clear)

    def test_final_stop_requires_fresh_full_teardown_gate(self) -> None:
        gate = function_block(self.driver, "final_stop_prereqs_verified")
        for token in (
            "prove_gateway_absent",
            "FAILS",
            "UNKNOWNS",
            "OWNED_UNIT",
            "UNIT_MUTATION_STARTED",
            "ABSENCE_STATE",
            "ABSENCE_OK",
            "probe_linger",
            "LINGER_CHANGED",
            "HOME_OWNED",
            "VM_REBOOTING",
            "VM_TRANSITION_KIND",
            "vm_status",
            "FINAL_STOP_GATE=OK",
        ):
            self.assertIn(token, gate)
        final = self.driver.split('banner "O — restore VM run-state', 1)[0]
        call = final.rindex("final_stop_prereqs_verified")
        remove = final.rindex('remove_owned_home_verified "after-uninstall"')
        self.assertLess(remove, call)
        self.assertIn("exit 1", final[call:])

    def test_linger_restore_requires_owned_marker_identity(self) -> None:
        probe = function_block(self.driver, "probe_linger")
        restore = function_block(self.driver, "restore_linger_verified")
        self.assertIn("stat.S_ISREG", probe)
        self.assertIn("LINGER_CUR_DEV", probe)
        self.assertIn("LINGER_CUR_INO", probe)
        self.assertIn("LINGER_OWNED_DEV", self.driver)
        self.assertIn("LINGER_OWNED_INO", self.driver)
        self.assertIn('"$LINGER_CUR_DEV:$LINGER_CUR_INO" != "$LINGER_OWNED_DEV:$LINGER_OWNED_INO"', restore)
        self.assertIn("refusing disable-linger", restore)

    def test_cleanup_never_mutates_unit_after_absence_probe_error(self) -> None:
        probe = function_block(self.driver, "prove_gateway_absent")
        cleanup = function_block(self.driver, "cleanup")
        self.assertIn("ABSENCE_STATE=ERROR", probe)
        self.assertIn("ABSENCE_STATE=PRESENT", probe)
        self.assertIn("ABSENCE_STATE=ABSENT", probe)
        self.assertIn('case "$ABSENCE_STATE" in', cleanup)
        error_branch = cleanup.split("ERROR)", 1)[1].split(";;", 1)[0]
        self.assertNotIn("hermes gateway stop", error_branch)
        self.assertNotIn("hermes gateway uninstall", error_branch)
        self.assertIn("refusing unit mutation", error_branch)

    def test_owned_home_cleanup_is_identity_gated_and_fd_safe(self) -> None:
        cleanup = function_block(self.driver, "remove_owned_home_verified")
        self.assertIn("HOME_DEV", self.driver)
        self.assertIn("HOME_INO", self.driver)
        self.assertIn("OWNED_HOME_IDENTITY_MATCH", cleanup)
        self.assertIn("os.O_NOFOLLOW", cleanup)
        self.assertIn("os.fstat(rootfd)", cleanup)
        self.assertIn("src_dir_fd=pfd", cleanup)
        self.assertIn("os.scandir(fd)", cleanup)
        self.assertIn("os.unlink(entry.name, dir_fd=fd)", cleanup)
        self.assertNotIn("rm -rf", cleanup)
        self.assertNotIn("shutil.rmtree", cleanup)

    def test_registration_probes_require_exact_zero_exit(self) -> None:
        self.assertNotIn("rc >= 2", self.driver)
        self.assertNotIn("returncode >= 2", self.driver)
        self.assertNotIn('"$SYS_RC" -ge 2', self.driver)
        self.assertNotIn('"$LAST_EC" -ge 2', self.driver)
        self.assertNotIn("rc >= 2", self.harness)
        self.assertIn("rc != 0", self.driver)
        self.assertIn("returncode != 0", self.driver)
        self.assertIn("rc != 0", self.harness)

    def test_named_install_and_cleanup_interruptions_are_runnable(self) -> None:
        install_points = ("install-begin", "post-install", "pre-start")
        cleanup_points = (
            "cleanup-stop",
            "cleanup-uninstall",
            "cleanup-home-remove",
            "cleanup-linger-restore",
            "cleanup-vm-start",
            "cleanup-vm-readiness",
            "cleanup-final-vm-query",
        )
        for point in install_points + cleanup_points:
            self.assertIn(point, self.driver, point)
            self.assertIn(point, self.harness, point)
        self.assertIn("external_presence", self.harness)
        self.assertIn("fixture_home_state", self.harness)

    def test_existing_home_teardown_uses_seed_time_identity_and_fd_safe_cleanup(self) -> None:
        cleanup = function_block(self.harness, "remove_seeded_home_verified")
        for token in (
            "HOME_DIR_DEV",
            "HOME_DIR_INO",
            "HOME_FILE_DEV",
            "HOME_FILE_INO",
            "HOME_FILE_SHA256",
            "os.O_NOFOLLOW",
            "os.fstat",
            "dir_fd=",
            "hashlib.sha256",
        ):
            self.assertIn(token, cleanup)
        scenario = self.harness[
            self.harness.index("existing-home)") : self.harness.index("active-foreign)")
        ]
        self.assertIn("SEEDED_HOME_ID=", scenario)
        self.assertIn("remove_seeded_home_verified", scenario)
        self.assertNotIn("repair_owned_fixture", scenario)

    def test_foreign_service_and_console_snapshots_pin_process_identity(self) -> None:
        active = self.harness[
            self.harness.index("active-foreign)") : self.harness.index("dangling-unit-link)")
        ]
        global_foreign = self.harness[
            self.harness.index("global-foreign)") : self.harness.index("transient-foreign)")
        ]
        transient = self.harness[
            self.harness.index("transient-foreign)") : self.harness.index("console-process-foreign)")
        ]
        console = self.harness[
            self.harness.index("console-process-foreign)") : self.harness.index(
                "inject-fail|inject-unknown|inject-reboot|install-begin"
            )
        ]
        snapshot = function_block(self.harness, "service_identity_snapshot")
        for token in ("InvocationID", "STARTTIME", "FragmentPath", "ControlGroup"):
            self.assertIn(token, snapshot)
        for scenario in (active, global_foreign, transient):
            self.assertGreaterEqual(scenario.count("service_identity_snapshot"), 2)
        self.assertGreaterEqual(console.count("CONSOLE_START"), 3)
        self.assertIn("starttime", console.lower())

    def test_harness_has_no_generic_service_or_recursive_home_repair(self) -> None:
        repair = function_block(self.harness, "repair_owned_fixture")
        self.assertNotIn("hermes gateway stop", repair)
        self.assertNotIn("hermes gateway uninstall", repair)
        self.assertNotIn("rm -rf", repair)
        injected = self.harness[
            self.harness.index("inject-fail|inject-unknown|inject-reboot|install-begin") :
        ]
        self.assertIn("FORENSIC_RETENTION", injected)
        self.assertIn("cleanup-stop|cleanup-uninstall", injected)

    def test_global_fixture_never_creates_shared_systemd_parent(self) -> None:
        scenario = self.harness[
            self.harness.index("global-foreign)") : self.harness.index("transient-foreign)")
        ]
        self.assertNotIn("install -d -m 0755 /etc/systemd/user", scenario)
        self.assertIn("GLOBAL_PARENT=READY", scenario)

    def test_harness_external_absence_disambiguates_missing_user_bus(self) -> None:
        proof = function_block(self.harness, "external_absence")
        self.assertIn("user_bus_present", proof)
        self.assertIn("systemd", proof)
        self.assertIn("--user", proof)
        self.assertIn("user-manager-without-bus", proof)

    def test_harness_readiness_timeout_fails(self) -> None:
        ensure = function_block(self.harness, "ensure_running")
        self.assertTrue(ensure.rstrip().endswith("}"))
        self.assertIn("return 1", ensure)

    def test_harness_compares_driver_exit_with_expected_exit(self) -> None:
        run = function_block(self.harness, "run_driver")
        self.assertIn("EXPECTED_EXIT", run)
        self.assertIn("DRIVER_EXIT", run)
        self.assertIn("hfail", run)
        self.assertIn("HARNESS_FAILS", self.harness)

    def test_harness_snapshots_vm_before_forensic_retention_decision(self) -> None:
        injected = self.harness[self.harness.index("inject-fail|inject-unknown|inject-reboot|install-begin") :]
        snapshot = injected.index("POST_VM_BEFORE_REPAIR")
        retention = injected.index("FORENSIC_RETENTION=", snapshot)
        self.assertLess(snapshot, retention)
        self.assertNotIn("repair_owned_fixture", injected[snapshot:])

    def test_crash_kill_uses_fresh_pidfd_full_identity_gate(self) -> None:
        helper = function_block(self.driver, "kill_owned_gateway_verified")
        for token in (
            "probe_owned_unit",
            "os.pidfd_open",
            "signal.pidfd_send_signal",
            "InvocationID",
            "MainPID",
            "ControlGroup",
            "starttime",
            "/proc/%d/environ",
            "HERMES_HOME",
        ):
            self.assertIn(token, helper)
        crash = self.driver.split('banner "K — crash recovery', 1)[1].split(
            'banner "L — native graceful restart', 1
        )[0]
        self.assertIn('kill_owned_gateway_verified "$MAINPID1" "$INVOC1" "$CG1"', crash)
        self.assertNotIn('hx "kill -KILL', crash)

    def test_run_driver_refuses_transport_false_green(self) -> None:
        run = function_block(self.harness, "run_driver")
        for token in ("DRIVER_RAN=0", "DRIVER_RAN=1", "mktemp failed", "log render failed", "log removal failed"):
            self.assertIn(token, run)
        self.assertLess(run.index("DRIVER_RAN=1"), run.index('env "S2_RUN_ID='))
        self.assertLess(run.index('if [ "$DRIVER_RAN" -ne 1 ]'), run.index('if [ "$DRIVER_EXIT" = "$EXPECTED_EXIT" ]'))

    def test_foreign_stop_proves_postcondition_before_success(self) -> None:
        stop = function_block(self.harness, "stop_owned_foreign_service_verified")
        mutation = stop.index('["systemctl", "--user", "stop"')
        success = stop.index('print("FOREIGN_STOP=OK")')
        post = stop[mutation:success]
        for token in ("ActiveState", "SubState", "MainPID", "ControlGroup", "cgroup.procs", "post-stop"):
            self.assertIn(token, post)
        self.assertIn('a["ControlGroup"]', post)

    def test_dangling_fixture_teardown_is_identity_and_fd_relative(self) -> None:
        scenario = self.harness[
            self.harness.index("dangling-unit-link)") : self.harness.index("unreadable-search-path)")
        ]
        for token in (
            "DANGLING_ID=",
            "PARENT_IDS=",
            "os.O_NOFOLLOW",
            "os.lstat",
            "os.fstat",
            "os.unlink(name, dir_fd=pfd)",
            "os.rmdir(name, dir_fd=pfd)",
        ):
            self.assertIn(token, scenario)
        self.assertNotIn('rm -f -- "$DPATH"', scenario)


class S2HarnessBehavior(unittest.TestCase):
    def test_unit_file_identity_mismatch_never_unlinks_fixture(self) -> None:
        helper = function_block(HARNESS.read_text(), "unlink_owned_unit_file_verified")
        with tempfile.TemporaryDirectory() as td:
            fixture = Path(td) / "foreign.service"
            fixture.write_bytes(b"foreign fixture\n")
            helper = helper.replace(
                "/home/hermes/.config/systemd/user/hermes-gateway.service", str(fixture)
            )
            st = fixture.stat()
            wrong = f"UNIT_FILE_ID={st.st_dev}:{st.st_ino}:" + "0" * 64
            shell = helper + "\n" + textwrap.dedent(
                f"""
                as_hermes() {{ bash -s; }}
                hfail() {{ :; }}
                unlink_owned_unit_file_verified {fixture!s} hermes {wrong}
                """
            )
            result = subprocess.run(
                ["bash", "-c", shell], text=True, capture_output=True, timeout=10
            )
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(fixture.read_bytes(), b"foreign fixture\n")
            self.assertIn("sha256", result.stdout + result.stderr)

    def test_service_identity_mismatch_never_reaches_stop_mutation(self) -> None:
        helper = function_block(HARNESS.read_text(), "stop_owned_foreign_service_verified")
        with tempfile.TemporaryDirectory() as td:
            bindir = Path(td)
            mutation_log = bindir / "stop-called"
            sleeper = subprocess.Popen(["sleep", "30"])
            try:
                fake = bindir / "systemctl"
                fake.write_text(
                    textwrap.dedent(
                        f"""\
                        #!/usr/bin/env bash
                        case " $* " in
                          *" show "*)
                            printf '%s\\n' \\
                              'ActiveState=active' 'SubState=running' 'MainPID={sleeper.pid}' \\
                              'InvocationID=0123456789abcdef0123456789abcdef' \\
                              'FragmentPath=/tmp/foreign.service' 'ControlGroup=/user.slice/test'
                            ;;
                          *" stop "*) printf called > {mutation_log!s}; exit 0 ;;
                          *) exit 9 ;;
                        esac
                        """
                    )
                )
                fake.chmod(fake.stat().st_mode | stat.S_IXUSR)
                shell = helper + "\n" + textwrap.dedent(
                    """
                    as_hermes() { bash -s; }
                    hfail() { :; }
                    stop_owned_foreign_service_verified 'SERVICE_ID=OK deliberately-wrong'
                    """
                )
                env = os.environ.copy()
                env["PATH"] = f"{bindir}:{env['PATH']}"
                result = subprocess.run(
                    ["bash", "-c", shell], env=env, text=True, capture_output=True, timeout=10
                )
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertFalse(mutation_log.exists(), "identity mismatch reached systemctl stop")
                self.assertIn("identity-mismatch", result.stdout + result.stderr)
            finally:
                sleeper.terminate()
                sleeper.wait(timeout=5)

    def test_clean_scenario_exits_nonzero_when_driver_returns_three(self) -> None:
        """A clean scenario expecting 0 must not false-green on DRIVER_EXIT=3."""
        with tempfile.TemporaryDirectory() as td:
            bindir = Path(td)
            fake = bindir / "limactl"
            fake.write_text(
                textwrap.dedent(
                    """\
                    #!/usr/bin/env bash
                    set -u
                    case "${1:-}" in
                      list) printf 'Running\\n' ;;
                      shell)
                        payload=$(cat)
                        case "$payload" in
                          *EXTERNAL_ABSENCE=OK*) printf 'EXTERNAL_ABSENCE=OK\\n' ;;
                          *FIXTURE_HOME=ABSENT*) printf 'FIXTURE_HOME=ABSENT\\n' ;;
                          *'print("disabled")'*) printf 'disabled\\n' ;;
                          *) printf 'HOME_PRESENT\\n' ;;
                        esac
                        ;;
                      start|stop) exit 0 ;;
                      *) exit 0 ;;
                    esac
                    """
                )
            )
            fake.chmod(fake.stat().st_mode | stat.S_IXUSR)
            env = os.environ.copy()
            env["PATH"] = f"{bindir}:{env['PATH']}"
            result = subprocess.run(
                ["bash", str(HARNESS), "clean"],
                cwd=ROOT,
                env=env,
                text=True,
                capture_output=True,
                timeout=20,
            )
            output = result.stdout + result.stderr
            self.assertIn("DRIVER_EXIT=3", output)
            self.assertNotEqual(
                result.returncode,
                0,
                msg=f"harness false-green:\n{output}",
            )


if __name__ == "__main__":
    unittest.main(verbosity=2)
