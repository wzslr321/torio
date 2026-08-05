#!/usr/bin/env python3
"""Run one argv vector with a wall-clock limit and no command shell."""

from __future__ import annotations

import os
import signal
import subprocess
import sys
import time


def process_group_exists(pgid: int) -> bool:
    try:
        os.killpg(pgid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def terminate_group(process: subprocess.Popen[bytes]) -> None:
    """Terminate the isolated child process group with bounded escalation."""
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return

    deadline = time.monotonic() + 0.1
    while process_group_exists(process.pid) and time.monotonic() < deadline:
        process.poll()
        time.sleep(0.05)

    if process_group_exists(process.pid):
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass

    try:
        process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=2)


def main(argv: list[str]) -> int:
    if len(argv) < 3:
        print("usage: with_timeout.py SECONDS COMMAND [ARG ...]", file=sys.stderr)
        return 2
    try:
        seconds = float(argv[1])
    except ValueError:
        print(f"invalid timeout: {argv[1]!r}", file=sys.stderr)
        return 2
    if seconds <= 0:
        print("timeout must be positive", file=sys.stderr)
        return 2

    try:
        process = subprocess.Popen(argv[2:], start_new_session=True)
    except OSError as exc:
        print(f"could not execute command: {exc}", file=sys.stderr)
        return 127

    def handle_signal(signum: int, _frame: object) -> None:
        terminate_group(process)
        raise SystemExit(128 + signum)

    for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(signum, handle_signal)

    try:
        return process.wait(timeout=seconds)
    except subprocess.TimeoutExpired:
        terminate_group(process)
        print(f"command timed out after {seconds:g}s", file=sys.stderr)
        return 124


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
