# owner: platform-team
"""Compress the waiting in a screen recording without touching the rest.

A recording of Torio spends real time waiting: the hub proves what the guest
holds before it will name a step, and each screen is a read of the box. On a
terminal that wait is information. In a file someone watches once it is the
reason they stop watching, so this takes every stretch where the screen is not
changing, keeps the beginning of it at real speed, and fast-forwards the rest.

Nothing is reordered, nothing is cut, and no frame is invented: a still stretch
is played fast, so the spinner in it spins fast. What the recording shows and
the order it shows it in are what the tape recorded.

Stillness is measured on a small greyscale copy of each frame, which is what
makes a spinner count as still: one character cell changing in a 160-pixel-wide
frame moves the average by a hundredth of a level, while switching screens moves
it by several. The threshold sits between those two by an order of magnitude in
both directions, and `--stats` prints the distribution the choice is made from.

Usage:
    python3 scripts/compress_demo_waits.py docs/demo/hub-raw.mp4 \
        --mp4 docs/demo/hub.mp4 --gif docs/demo/hub.gif
"""

from __future__ import annotations

import argparse
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from fractions import Fraction
from pathlib import Path

# The greyscale copy every frame is compared on. Small enough that a one-cell
# spinner is a rounding error, large enough that a screen switch is not.
PROBE_WIDTH = 160

# Mean absolute difference, in levels of 255, below which two consecutive frames
# are the same picture. Calibrated from --stats on the hub recording: spinner
# ticks land near 0.02 and screen changes near 1.0 and above.
STILL_THRESHOLD = 0.30


@dataclass(frozen=True)
class Segment:
    """A run of source frames and the rate it is played back at."""

    start: int
    end: int  # exclusive
    speed: float


def ffprobe(path: Path) -> tuple[Fraction, int, int]:
    out = subprocess.run(
        [
            "ffprobe", "-v", "error", "-select_streams", "v:0",
            "-show_entries", "stream=width,height,r_frame_rate",
            "-of", "default=nw=1:nk=1", str(path),
        ],
        check=True, capture_output=True, text=True,
    ).stdout.split()
    width, height, rate = int(out[0]), int(out[1]), Fraction(out[2])
    return rate, width, height


def frame_differences(path: Path, height: int, width: int) -> list[float]:
    """Mean absolute difference between each frame and the one before it."""
    probe_h = max(2, round(height * PROBE_WIDTH / width / 2) * 2)
    raw = subprocess.run(
        [
            "ffmpeg", "-v", "error", "-i", str(path),
            "-vf", f"scale={PROBE_WIDTH}:{probe_h},format=gray",
            "-f", "rawvideo", "-pix_fmt", "gray", "-",
        ],
        check=True, capture_output=True,
    ).stdout

    size = PROBE_WIDTH * probe_h
    count = len(raw) // size
    diffs: list[float] = []
    previous = raw[0:size]
    for i in range(1, count):
        current = raw[i * size : (i + 1) * size]
        total = sum(abs(a - b) for a, b in zip(current, previous))
        diffs.append(total / size)
        previous = current
    return diffs


def plan(
    diffs: list[float],
    fps: float,
    hold_reading: float,
    hold_waiting: float,
    speed: float,
    threshold: float,
    motion: float,
    busy_fraction: float,
) -> list[Segment]:
    """Decide the playback rate of every frame.

    A still run keeps some seconds at real speed before it is fast-forwarded,
    and how many depends on what kind of stillness it is. A stretch with a
    spinner in it is a wait: something is turning at several ticks a second
    while the rest of the frame holds, and nobody wants to watch it. A stretch
    where nothing moves at all is a screen that has settled, which is the only
    chance a viewer gets to read it, so it keeps longer.

    Both are stillness by the same threshold. What separates them is how often
    anything at all changed inside the run.
    """
    still = [d < threshold for d in diffs]
    reading_frames = max(1, round(hold_reading * fps))
    waiting_frames = max(1, round(hold_waiting * fps))

    rates: list[float] = [1.0] * (len(diffs) + 1)
    run_start: int | None = None
    for i, quiet in enumerate([*still, False]):
        if quiet and run_start is None:
            run_start = i
            continue
        if quiet:
            continue
        if run_start is not None:
            run = diffs[run_start:i]
            moving = sum(1 for d in run if d > motion)
            busy = run and moving / len(run) >= busy_fraction
            hold_frames = waiting_frames if busy else reading_frames
            for j in range(run_start + hold_frames, i):
                # +1: diff j describes the step from frame j to frame j+1.
                rates[j + 1] = speed
            run_start = None

    segments: list[Segment] = []
    start = 0
    for i in range(1, len(rates) + 1):
        if i == len(rates) or rates[i] != rates[start]:
            segments.append(Segment(start, i, rates[start]))
            start = i
    return segments


def filtergraph(segments: list[Segment], fps: Fraction) -> str:
    parts: list[str] = []
    labels: list[str] = []
    for i, seg in enumerate(segments):
        label = f"v{i}"
        labels.append(f"[{label}]")
        parts.append(
            f"[0:v]trim=start_frame={seg.start}:end_frame={seg.end},"
            f"setpts=(PTS-STARTPTS)/{seg.speed}[{label}]"
        )
    joined = "".join(labels)
    parts.append(f"{joined}concat=n={len(segments)}:v=1:a=0,fps={fps}[out]")
    return ";".join(parts)


def write_mp4(source: Path, graph: str, target: Path) -> None:
    _ = subprocess.run(
        [
            "ffmpeg", "-v", "error", "-y", "-i", str(source),
            "-filter_complex", graph, "-map", "[out]",
            "-c:v", "libx264", "-preset", "slow", "-crf", "20",
            "-pix_fmt", "yuv420p", "-movflags", "+faststart", str(target),
        ],
        check=True,
    )


def write_gif(source: Path, graph: str, target: Path) -> None:
    """Render the GIF from the same source and plan as the MP4.

    Not from the finished MP4: that one is inter-frame coded, so a screen that
    did not change comes back slightly different every frame, and a GIF of it
    stores every one of them. Rendering from the source instead lets
    `mpdecimate` collapse a settled screen into one frame with a long delay,
    which is the difference between a file of a megabyte and a third of one.

    A terminal has flat colours and no gradients, so the palette is small and
    dithering would only add noise for the encoder to store.
    """
    with tempfile.TemporaryDirectory() as tmp:
        palette = Path(tmp) / "palette.png"
        _ = subprocess.run(
            ["ffmpeg", "-v", "error", "-y", "-i", str(source),
             "-filter_complex",
             f"{graph};[out]mpdecimate,palettegen=stats_mode=diff:max_colors=64[pal]",
             "-map", "[pal]", str(palette)],
            check=True,
        )
        _ = subprocess.run(
            ["ffmpeg", "-v", "error", "-y", "-i", str(source), "-i", str(palette),
             "-filter_complex",
             f"{graph};[out]mpdecimate[d];[d][1:v]paletteuse=dither=none:diff_mode=rectangle[gif]",
             "-map", "[gif]", "-fps_mode", "vfr", "-loop", "0", str(target)],
            check=True,
        )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path, help="the recording as vhs wrote it")
    parser.add_argument("--mp4", type=Path, help="where to write the compressed MP4")
    parser.add_argument("--gif", type=Path, help="where to write the compressed GIF")
    parser.add_argument("--hold-reading", type=float, default=2.5,
                        help="seconds a settled screen is held at real speed")
    parser.add_argument("--hold-waiting", type=float, default=1.0,
                        help="seconds a spinner is held at real speed")
    parser.add_argument("--speed", type=float, default=20.0,
                        help="rate the rest of a still stretch is played at")
    parser.add_argument("--motion", type=float, default=0.0005,
                        help="frame difference that counts as something moving")
    parser.add_argument("--busy-fraction", type=float, default=0.2,
                        help="share of moving frames that makes a stretch a wait")
    parser.add_argument("--threshold", type=float, default=STILL_THRESHOLD,
                        help="mean frame difference below which the screen is still")
    parser.add_argument("--stats", action="store_true",
                        help="print the frame-difference distribution and stop")
    args = parser.parse_args()

    for tool in ("ffmpeg", "ffprobe"):
        if shutil.which(tool) is None:
            print(f"{tool} is not on PATH", file=sys.stderr)
            return 2
    if not args.source.is_file():
        print(f"no such recording: {args.source}", file=sys.stderr)
        return 2

    rate, width, height = ffprobe(args.source)
    diffs = frame_differences(args.source, height, width)
    fps = float(rate)

    if args.stats:
        ordered = sorted(diffs)
        for label, index in (("min", 0), ("p50", len(ordered) // 2),
                             ("p90", len(ordered) * 9 // 10),
                             ("p99", len(ordered) * 99 // 100),
                             ("max", len(ordered) - 1)):
            print(f"{label}: {ordered[index]:.4f}")
        quiet = sum(1 for d in diffs if d < args.threshold)
        print(f"still at threshold {args.threshold}: {quiet}/{len(diffs)} frames")
        return 0

    segments = plan(diffs, fps, args.hold_reading, args.hold_waiting,
                    args.speed, args.threshold, args.motion, args.busy_fraction)
    kept = sum((s.end - s.start) / s.speed for s in segments)
    print(f"{len(diffs) + 1} frames in {(len(diffs) + 1) / fps:.1f}s "
          f"-> {kept / fps:.1f}s in {len(segments)} segments")

    graph = filtergraph(segments, rate)
    if args.mp4:
        write_mp4(args.source, graph, args.mp4)
        print(f"wrote {args.mp4}")
    if args.gif:
        write_gif(args.source, graph, args.gif)
        print(f"wrote {args.gif}")
    if not args.mp4 and not args.gif:
        print("nothing to write: pass --mp4, --gif, or both", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
