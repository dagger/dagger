#!/usr/bin/env python3

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any


DEFAULT_STARTUP_WARN = 2.0
DEFAULT_STARTUP_FAIL = 5.0
DEFAULT_IDLE_WARN = 10.0
DEFAULT_IDLE_FAIL = 30.0
DEFAULT_SEMANTIC_WARN = 120.0
SNAPSHOT_EPSILON = 0.000001

ANSI_ESCAPE_RE = re.compile(
    r"""
    \x1B
    (?:
        \[[0-?]*[ -/]*[@-~]
        |\][^\x07]*(?:\x07|\x1B\\)
        |[@-Z\\-_]
    )
    """,
    re.VERBOSE,
)


@dataclass
class CastEvent:
    delta: float
    kind: str
    data: Any
    raw: list[Any]
    time: float


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    try:
        return args.func(args)
    except QaError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1


class QaError(RuntimeError):
    pass


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Record and analyze terminal QA sessions with asciinema.",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    record = subparsers.add_parser("record", help="Record a terminal session to a cast file.")
    add_common_record_args(record)
    record.set_defaults(func=cmd_record)

    snapshot = subparsers.add_parser(
        "snapshot",
        help="Render the visible terminal screen at a given timestamp.",
    )
    snapshot.add_argument("cast", type=Path, help="Path to an asciinema cast file.")
    snapshot.add_argument("--at", type=float, required=True, help="Timestamp in seconds.")
    snapshot.add_argument("--output", type=Path, help="Output path for the text snapshot.")
    snapshot.add_argument("--label", help="Optional label for the snapshot file name.")
    snapshot.set_defaults(func=cmd_snapshot)

    analyze = subparsers.add_parser("analyze", help="Analyze a recorded cast.")
    analyze.add_argument("cast", type=Path, help="Path to an asciinema cast file.")
    analyze.add_argument(
        "--snapshot-at",
        action="append",
        default=[],
        type=float,
        help="Additional timestamp to snapshot. Repeat as needed.",
    )
    analyze.add_argument(
        "--milestone",
        action="append",
        default=[],
        help="Regex describing a meaningful progress milestone. Repeat as needed.",
    )
    analyze.add_argument(
        "--startup-warn",
        type=float,
        default=DEFAULT_STARTUP_WARN,
        help="Warn if first output takes at least this many seconds.",
    )
    analyze.add_argument(
        "--startup-fail",
        type=float,
        default=DEFAULT_STARTUP_FAIL,
        help="Fail if first output takes at least this many seconds.",
    )
    analyze.add_argument(
        "--idle-warn",
        type=float,
        default=DEFAULT_IDLE_WARN,
        help="Warn on no-output gaps of at least this many seconds.",
    )
    analyze.add_argument(
        "--idle-fail",
        type=float,
        default=DEFAULT_IDLE_FAIL,
        help="Fail on no-output gaps of at least this many seconds.",
    )
    analyze.add_argument(
        "--semantic-warn",
        type=float,
        default=DEFAULT_SEMANTIC_WARN,
        help="Warn when output continues without milestone progress for this long.",
    )
    analyze.add_argument(
        "--output-dir",
        type=Path,
        help="Directory for generated analysis artifacts. Defaults to the cast directory.",
    )
    analyze.set_defaults(func=cmd_analyze)

    run = subparsers.add_parser(
        "run",
        help="Record a terminal session and analyze it in one step.",
    )
    add_common_record_args(run)
    run.add_argument(
        "--snapshot-at",
        action="append",
        default=[],
        type=float,
        help="Additional timestamp to snapshot. Repeat as needed.",
    )
    run.add_argument(
        "--milestone",
        action="append",
        default=[],
        help="Regex describing a meaningful progress milestone. Repeat as needed.",
    )
    run.add_argument("--startup-warn", type=float, default=DEFAULT_STARTUP_WARN)
    run.add_argument("--startup-fail", type=float, default=DEFAULT_STARTUP_FAIL)
    run.add_argument("--idle-warn", type=float, default=DEFAULT_IDLE_WARN)
    run.add_argument("--idle-fail", type=float, default=DEFAULT_IDLE_FAIL)
    run.add_argument("--semantic-warn", type=float, default=DEFAULT_SEMANTIC_WARN)
    run.set_defaults(func=cmd_run)

    return parser


def add_common_record_args(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--name", required=True, help="Artifact name prefix.")
    parser.add_argument(
        "--command",
        help="Command to run inside the recording. Omit for an interactive session.",
    )
    parser.add_argument(
        "--artifact-dir",
        type=Path,
        help="Artifact directory. Defaults to .qa/tui/<name>-<timestamp>.",
    )
    parser.add_argument(
        "--capture-input",
        action="store_true",
        help="Record keyboard input as well as output.",
    )
    parser.add_argument(
        "--workdir",
        type=Path,
        help="Working directory for the recorded command.",
    )
    parser.add_argument(
        "--window-size",
        help="Override terminal size as COLSxROWS.",
    )
    parser.add_argument(
        "--idle-time-limit",
        type=float,
        help="Optional playback idle-time cap stored in the cast metadata.",
    )
    parser.add_argument(
        "--headless",
        action="store_true",
        help="Force headless recording mode.",
    )


def cmd_record(args: argparse.Namespace) -> int:
    artifacts = ensure_artifact_dir(args.name, args.artifact_dir)
    result = record_session(
        artifact_dir=artifacts,
        name=args.name,
        command=args.command,
        workdir=args.workdir,
        capture_input=args.capture_input,
        window_size=args.window_size,
        idle_time_limit=args.idle_time_limit,
        headless=args.headless,
    )
    print(result["cast"])
    if result.get("command_exit_code") not in (None, 0):
        print(
            f"recorded command exit code: {result['command_exit_code']}",
            file=sys.stderr,
        )
    return 0


def cmd_snapshot(args: argparse.Namespace) -> int:
    cast_path = args.cast.resolve()
    if not cast_path.is_file():
        raise QaError(f"cast not found: {cast_path}")
    output_path = args.output
    if output_path is None:
        label = sanitize_label(args.label or f"at-{format_seconds(args.at)}")
        output_path = cast_path.parent / "snapshots" / f"{label}.txt"
    render_snapshot(cast_path, args.at, output_path)
    print(output_path.resolve())
    return 0


def cmd_analyze(args: argparse.Namespace) -> int:
    report = analyze_cast(
        cast_path=args.cast.resolve(),
        output_dir=(args.output_dir.resolve() if args.output_dir else None),
        snapshot_times=list(args.snapshot_at),
        milestone_patterns=list(args.milestone),
        startup_warn=args.startup_warn,
        startup_fail=args.startup_fail,
        idle_warn=args.idle_warn,
        idle_fail=args.idle_fail,
        semantic_warn=args.semantic_warn,
    )
    print(report["report_md"])
    return 0


def cmd_run(args: argparse.Namespace) -> int:
    artifacts = ensure_artifact_dir(args.name, args.artifact_dir)
    record_session(
        artifact_dir=artifacts,
        name=args.name,
        command=args.command,
        workdir=args.workdir,
        capture_input=args.capture_input,
        window_size=args.window_size,
        idle_time_limit=args.idle_time_limit,
        headless=args.headless,
    )
    report = analyze_cast(
        cast_path=artifacts / "session.cast",
        output_dir=artifacts,
        snapshot_times=list(args.snapshot_at),
        milestone_patterns=list(args.milestone),
        startup_warn=args.startup_warn,
        startup_fail=args.startup_fail,
        idle_warn=args.idle_warn,
        idle_fail=args.idle_fail,
        semantic_warn=args.semantic_warn,
    )
    summary = report["summary"]
    print(f"artifacts: {artifacts}")
    print(f"verdict: {summary['verdict']}")
    print(f"report: {report['report_md']}")
    return 0


def ensure_artifact_dir(name: str, artifact_dir: Path | None) -> Path:
    if artifact_dir is None:
        stamp = datetime.now().strftime("%Y%m%d-%H%M%S")
        artifact_dir = Path(".qa") / "tui" / f"{sanitize_label(name)}-{stamp}"
    artifact_dir = artifact_dir.resolve()
    artifact_dir.mkdir(parents=True, exist_ok=True)
    (artifact_dir / "snapshots").mkdir(parents=True, exist_ok=True)
    return artifact_dir


def record_session(
    *,
    artifact_dir: Path,
    name: str,
    command: str | None,
    workdir: Path | None,
    capture_input: bool,
    window_size: str | None,
    idle_time_limit: float | None,
    headless: bool,
) -> dict[str, Any]:
    asciinema = shutil.which("asciinema")
    if not asciinema:
        raise QaError("asciinema is required but was not found in PATH")

    cast_path = artifact_dir / "session.cast"
    meta_path = artifact_dir / "meta.json"
    workdir_path = workdir.resolve() if workdir else None
    should_headless = headless or not (sys.stdin.isatty() and sys.stdout.isatty())
    effective_window_size = window_size or infer_window_size()

    cmd = [asciinema, "record", "--quiet", "--overwrite", "--return"]
    if command:
        cmd.extend(["--command", command])
    if capture_input:
        cmd.append("--capture-input")
    if idle_time_limit is not None:
        cmd.extend(["--idle-time-limit", str(idle_time_limit)])
    if should_headless:
        cmd.append("--headless")
    if effective_window_size:
        cmd.extend(["--window-size", effective_window_size])
    cmd.extend(["--title", name, str(cast_path)])

    started_at = datetime.now().astimezone().isoformat()
    result = subprocess.run(
        cmd,
        cwd=str(workdir_path) if workdir_path else None,
        text=True,
        capture_output=True,
        check=False,
    )
    finished_at = datetime.now().astimezone().isoformat()

    if not cast_path.exists():
        stderr = result.stderr.strip()
        if stderr:
            raise QaError(stderr)
        raise QaError("recording failed before a cast file was produced")

    meta = {
        "name": name,
        "command": command,
        "workdir": str(workdir_path) if workdir_path else None,
        "artifact_dir": str(artifact_dir),
        "cast": str(cast_path),
        "capture_input": capture_input,
        "headless": should_headless,
        "window_size": effective_window_size,
        "idle_time_limit": idle_time_limit,
        "started_at": started_at,
        "finished_at": finished_at,
        "command_exit_code": result.returncode,
        "stderr": result.stderr,
    }
    meta_path.write_text(json.dumps(meta, indent=2, sort_keys=True) + "\n")
    return meta


def analyze_cast(
    *,
    cast_path: Path,
    output_dir: Path | None,
    snapshot_times: list[float],
    milestone_patterns: list[str],
    startup_warn: float,
    startup_fail: float,
    idle_warn: float,
    idle_fail: float,
    semantic_warn: float,
) -> dict[str, Any]:
    if not cast_path.is_file():
        raise QaError(f"cast not found: {cast_path}")
    output_dir = (output_dir or cast_path.parent).resolve()
    output_dir.mkdir(parents=True, exist_ok=True)
    snapshots_dir = output_dir / "snapshots"
    snapshots_dir.mkdir(parents=True, exist_ok=True)

    header, events = load_cast(cast_path)
    total_duration = events[-1].time if events else 0.0
    output_events = [event for event in events if event.kind == "o"]
    output_times = [event.time for event in output_events]
    first_output_time = output_times[0] if output_times else None
    last_output_time = output_times[-1] if output_times else None
    exit_event = next((event for event in events if event.kind == "x"), None)
    exit_time = exit_event.time if exit_event else total_duration

    final_txt = output_dir / "final.txt"
    convert_cast_to_text(cast_path, final_txt)

    thresholds = {
        "startup_warn": startup_warn,
        "startup_fail": startup_fail,
        "idle_warn": idle_warn,
        "idle_fail": idle_fail,
        "semantic_warn": semantic_warn,
    }

    findings: list[dict[str, Any]] = []
    startup_duration = first_output_time if first_output_time is not None else total_duration
    startup_severity = severity_for_duration(startup_duration, startup_warn, startup_fail)
    if startup_severity != "pass":
        findings.append(
            finding(
                startup_severity,
                "startup_delay",
                f"First output took {format_seconds(startup_duration)}.",
            )
        )

    gaps = build_no_output_gaps(output_times, total_duration)
    idle_gaps = []
    max_no_output_gap = 0.0
    longest_gap = None
    for gap in gaps:
        max_no_output_gap = max(max_no_output_gap, gap["duration"])
        if longest_gap is None or gap["duration"] > longest_gap["duration"]:
            longest_gap = gap
        if gap["kind"] == "startup":
            continue
        gap_severity = severity_for_duration(gap["duration"], idle_warn, idle_fail)
        gap["severity"] = gap_severity
        if gap_severity != "pass":
            findings.append(
                finding(
                    gap_severity,
                    "idle_gap",
                    (
                        f"No output for {format_seconds(gap['duration'])} "
                        f"between {format_seconds(gap['start'])} and {format_seconds(gap['end'])}."
                    ),
                )
            )
            idle_gaps.append(gap)

    milestones = evaluate_milestones(output_events, milestone_patterns)
    semantic = evaluate_semantic_hangs(
        output_times=output_times,
        total_duration=total_duration,
        milestones=milestones,
        semantic_warn=semantic_warn,
    )
    for candidate in semantic["candidates"]:
        findings.append(
            finding(
                "warn",
                "semantic_hang",
                (
                    f"Output continued for {format_seconds(candidate['duration'])} "
                    f"without a milestone between {format_seconds(candidate['start'])} "
                    f"and {format_seconds(candidate['end'])}."
                ),
            )
        )

    meta = load_optional_json(cast_path.parent / "meta.json")
    if meta and meta.get("command_exit_code") not in (None, 0):
        findings.append(
            finding(
                "fail",
                "command_exit",
                f"Recorded command exited with status {meta['command_exit_code']}.",
            )
        )

    requested_snapshots = [
        snapshot_spec(at, f"at-{format_seconds(at)}") for at in snapshot_times
    ]
    auto_snapshots = []
    if first_output_time is not None:
        auto_snapshots.append(snapshot_spec(first_output_time, "first-output"))
    if longest_gap is not None and longest_gap["duration"] > 0:
        auto_snapshots.append(snapshot_spec(longest_gap["start"], "longest-idle-start"))
        if longest_gap["end"] > longest_gap["start"]:
            auto_snapshots.append(
                snapshot_spec(
                    max(longest_gap["start"], longest_gap["end"] - SNAPSHOT_EPSILON),
                    "longest-idle-end",
                )
            )
    auto_snapshots.append(snapshot_spec(total_duration, "final"))
    for index, candidate in enumerate(semantic["candidates"], start=1):
        auto_snapshots.append(snapshot_spec(candidate["start"], f"semantic-{index}-start"))
        auto_snapshots.append(
            snapshot_spec(
                max(candidate["start"], candidate["end"] - SNAPSHOT_EPSILON),
                f"semantic-{index}-end",
            )
        )

    snapshot_results = write_snapshots(
        cast_path=cast_path,
        snapshots_dir=snapshots_dir,
        final_txt=final_txt,
        specs=requested_snapshots + auto_snapshots,
        total_duration=total_duration,
    )

    if longest_gap is not None:
        mark_visual_change(
            gap=longest_gap,
            snapshot_results=snapshot_results,
            start_label="longest-idle-start",
            end_label="longest-idle-end",
        )
    for index, candidate in enumerate(semantic["candidates"], start=1):
        mark_visual_change(
            gap=candidate,
            snapshot_results=snapshot_results,
            start_label=f"semantic-{index}-start",
            end_label=f"semantic-{index}-end",
        )

    summary = {
        "verdict": summarize_verdict(findings),
        "total_duration": total_duration,
        "time_to_first_output": first_output_time,
        "time_to_exit": exit_time,
        "output_event_count": len(output_events),
        "max_no_output_gap": max_no_output_gap,
    }

    report = {
        "cast": str(cast_path),
        "header": header,
        "meta": meta,
        "thresholds": thresholds,
        "summary": summary,
        "startup": {
            "duration": startup_duration,
            "severity": startup_severity,
        },
        "idle_gaps": idle_gaps,
        "milestones": milestones,
        "semantic_hang": semantic,
        "snapshots": snapshot_results,
        "final_txt": str(final_txt),
        "findings": findings,
        "report_json": str((output_dir / "report.json").resolve()),
        "report_md": str((output_dir / "report.md").resolve()),
    }

    report_json_path = output_dir / "report.json"
    report_json_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    report_md_path = output_dir / "report.md"
    report_md_path.write_text(render_markdown_report(report) + "\n")
    return report


def build_no_output_gaps(output_times: list[float], total_duration: float) -> list[dict[str, Any]]:
    gaps = []
    if not output_times:
        return [
            {
                "kind": "startup",
                "start": 0.0,
                "end": total_duration,
                "duration": total_duration,
            }
        ]

    if output_times[0] > 0:
        gaps.append(
            {
                "kind": "startup",
                "start": 0.0,
                "end": output_times[0],
                "duration": output_times[0],
            }
        )

    for start, end in zip(output_times, output_times[1:]):
        gaps.append(
            {
                "kind": "idle",
                "start": start,
                "end": end,
                "duration": end - start,
            }
        )

    if total_duration > output_times[-1]:
        gaps.append(
            {
                "kind": "tail",
                "start": output_times[-1],
                "end": total_duration,
                "duration": total_duration - output_times[-1],
            }
        )

    return gaps


def evaluate_milestones(
    output_events: list[CastEvent],
    patterns: list[str],
) -> list[dict[str, Any]]:
    if not patterns:
        return []

    compiled = [re.compile(pattern) for pattern in patterns]
    hits = [None] * len(compiled)
    transcript = ""

    for event in output_events:
        transcript += normalize_output_text(str(event.data))
        for index, regex in enumerate(compiled):
            if hits[index] is None and regex.search(transcript):
                hits[index] = event.time

    result = []
    for pattern, hit in zip(patterns, hits):
        result.append(
            {
                "pattern": pattern,
                "matched": hit is not None,
                "time": hit,
            }
        )
    return result


def evaluate_semantic_hangs(
    *,
    output_times: list[float],
    total_duration: float,
    milestones: list[dict[str, Any]],
    semantic_warn: float,
) -> dict[str, Any]:
    if not milestones:
        return {
            "status": "not_evaluated",
            "candidates": [],
        }

    milestone_times = sorted(
        milestone["time"] for milestone in milestones if milestone["matched"] and milestone["time"] is not None
    )
    checkpoints = []
    if output_times:
        checkpoints.append(output_times[0])
    checkpoints.extend(milestone_times)
    checkpoints.append(total_duration)

    candidates = []
    for start, end in zip(checkpoints, checkpoints[1:]):
        if end - start < semantic_warn:
            continue
        if not any(start < time <= end for time in output_times):
            continue
        candidates.append(
            {
                "start": start,
                "end": end,
                "duration": end - start,
            }
        )

    return {
        "status": "warn" if candidates else "pass",
        "candidates": candidates,
    }


def write_snapshots(
    *,
    cast_path: Path,
    snapshots_dir: Path,
    final_txt: Path,
    specs: list[dict[str, Any]],
    total_duration: float,
) -> list[dict[str, Any]]:
    results = []
    seen_labels = set()
    for spec in specs:
        label = sanitize_label(spec["label"])
        if label in seen_labels:
            continue
        seen_labels.add(label)
        if label == "final":
            path = final_txt
        else:
            path = snapshots_dir / f"{label}.txt"
            render_snapshot(cast_path, min(spec["time"], total_duration), path)
        results.append(
            {
                "label": label,
                "time": spec["time"],
                "path": str(path.resolve()),
            }
        )
    return results


def mark_visual_change(
    *,
    gap: dict[str, Any],
    snapshot_results: list[dict[str, Any]],
    start_label: str,
    end_label: str,
) -> None:
    start_snapshot = next((item for item in snapshot_results if item["label"] == start_label), None)
    end_snapshot = next((item for item in snapshot_results if item["label"] == end_label), None)
    if not start_snapshot or not end_snapshot:
        return
    start_text = Path(start_snapshot["path"]).read_text()
    end_text = Path(end_snapshot["path"]).read_text()
    gap["screen_changed"] = normalize_snapshot_text(start_text) != normalize_snapshot_text(end_text)
    gap["start_snapshot"] = start_snapshot["path"]
    gap["end_snapshot"] = end_snapshot["path"]


def render_snapshot(cast_path: Path, at: float, output_path: Path) -> None:
    header, events = load_cast(cast_path)
    truncated = []
    for event in events:
        if event.time <= at + SNAPSHOT_EPSILON:
            truncated.append(event.raw)
        else:
            break
    output_path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile("w", suffix=".cast", delete=False) as handle:
        temp_cast = Path(handle.name)
        handle.write(json.dumps(header) + "\n")
        for raw in truncated:
            handle.write(json.dumps(raw) + "\n")
    try:
        convert_cast_to_text(temp_cast, output_path)
    finally:
        temp_cast.unlink(missing_ok=True)


def load_cast(path: Path) -> tuple[dict[str, Any], list[CastEvent]]:
    lines = path.read_text().splitlines()
    if not lines:
        raise QaError(f"cast is empty: {path}")
    try:
        header = json.loads(lines[0])
    except json.JSONDecodeError as exc:
        raise QaError(f"invalid cast header in {path}: {exc}") from exc

    events = []
    cumulative = 0.0
    for index, line in enumerate(lines[1:], start=2):
        if not line.strip():
            continue
        try:
            raw = json.loads(line)
        except json.JSONDecodeError as exc:
            raise QaError(f"invalid cast event on line {index}: {exc}") from exc
        if not isinstance(raw, list) or len(raw) < 3:
            raise QaError(f"invalid cast event on line {index}: expected [delta, type, data]")
        delta = float(raw[0])
        cumulative += delta
        events.append(
            CastEvent(
                delta=delta,
                kind=str(raw[1]),
                data=raw[2],
                raw=raw,
                time=cumulative,
            )
        )
    return header, events


def convert_cast_to_text(input_path: Path, output_path: Path) -> None:
    asciinema = shutil.which("asciinema")
    if not asciinema:
        raise QaError("asciinema is required but was not found in PATH")
    output_path.parent.mkdir(parents=True, exist_ok=True)
    result = subprocess.run(
        [asciinema, "convert", "--overwrite", str(input_path), str(output_path)],
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode != 0:
        stderr = result.stderr.strip()
        if stderr:
            raise QaError(stderr)
        raise QaError(f"failed to convert cast to text: {input_path}")


def finding(severity: str, kind: str, message: str) -> dict[str, str]:
    return {
        "severity": severity,
        "kind": kind,
        "message": message,
    }


def severity_for_duration(duration: float, warn_after: float, fail_after: float) -> str:
    if duration >= fail_after:
        return "fail"
    if duration >= warn_after:
        return "warn"
    return "pass"


def summarize_verdict(findings: list[dict[str, Any]]) -> str:
    severities = {item["severity"] for item in findings}
    if "fail" in severities:
        return "fail"
    if "warn" in severities:
        return "warn"
    return "pass"


def snapshot_spec(at: float, label: str) -> dict[str, Any]:
    return {
        "time": max(at, 0.0),
        "label": label,
    }


def load_optional_json(path: Path) -> dict[str, Any] | None:
    if not path.is_file():
        return None
    try:
        return json.loads(path.read_text())
    except json.JSONDecodeError:
        return None


def normalize_output_text(data: str) -> str:
    data = ANSI_ESCAPE_RE.sub("", data)
    buffer: list[str] = []
    for char in data:
        if char == "\r":
            buffer.append("\n")
        elif char == "\n" or char == "\t" or char >= " ":
            buffer.append(char)
        elif char == "\b":
            if buffer:
                buffer.pop()
        else:
            continue
    return "".join(buffer)


def normalize_snapshot_text(text: str) -> str:
    return "\n".join(line.rstrip() for line in text.strip().splitlines())


def render_markdown_report(report: dict[str, Any]) -> str:
    lines = ["# TUI QA Report", ""]
    meta = report.get("meta") or {}
    command = meta.get("command")
    if command:
        lines.append(f"Command: `{command}`")
        lines.append("")
    lines.append(f"Cast: `{report['cast']}`")
    lines.append(f"Verdict: **{report['summary']['verdict']}**")
    lines.append("")

    lines.append("## Timings")
    lines.append("")
    lines.append(f"- Total duration: {format_seconds(report['summary']['total_duration'])}")
    lines.append(
        "- Time to first output: "
        + (
            format_seconds(report['summary']['time_to_first_output'])
            if report['summary']['time_to_first_output'] is not None
            else "none"
        )
    )
    lines.append(f"- Time to exit: {format_seconds(report['summary']['time_to_exit'])}")
    lines.append(f"- Output events: {report['summary']['output_event_count']}")
    lines.append(
        f"- Max no-output gap: {format_seconds(report['summary']['max_no_output_gap'])}"
    )
    lines.append("")

    lines.append("## Findings")
    lines.append("")
    if report["findings"]:
        for item in report["findings"]:
            lines.append(f"- {item['severity'].upper()}: {item['message']}")
    else:
        lines.append("- PASS: No warnings or failures.")
    lines.append("")

    if report["milestones"]:
        lines.append("## Milestones")
        lines.append("")
        for milestone in report["milestones"]:
            if milestone["matched"]:
                lines.append(
                    f"- Matched `{milestone['pattern']}` at {format_seconds(milestone['time'])}"
                )
            else:
                lines.append(f"- Missing `{milestone['pattern']}`")
        lines.append("")

    if report["semantic_hang"]["status"] == "not_evaluated":
        lines.append("## Semantic Hang")
        lines.append("")
        lines.append("- Not evaluated. Supply one or more `--milestone` regexes.")
        lines.append("")
    elif report["semantic_hang"]["candidates"]:
        lines.append("## Semantic Hang")
        lines.append("")
        for candidate in report["semantic_hang"]["candidates"]:
            screen_note = ""
            if "screen_changed" in candidate:
                screen_note = (
                    " screen changed."
                    if candidate["screen_changed"]
                    else " screen did not materially change."
                )
            lines.append(
                "- "
                + (
                    f"{format_seconds(candidate['duration'])} without milestone progress "
                    f"from {format_seconds(candidate['start'])} to {format_seconds(candidate['end'])};"
                    f"{screen_note}"
                )
            )
        lines.append("")

    lines.append("## Artifacts")
    lines.append("")
    lines.append(f"- Final text: `{report['final_txt']}`")
    lines.append(f"- JSON report: `{report['report_json']}`")
    for snapshot in report["snapshots"]:
        lines.append(
            f"- Snapshot `{snapshot['label']}` at {format_seconds(snapshot['time'])}: "
            f"`{snapshot['path']}`"
        )

    return "\n".join(lines).rstrip()


def sanitize_label(label: str) -> str:
    return re.sub(r"[^A-Za-z0-9._-]+", "-", label).strip("-").lower() or "snapshot"


def format_seconds(value: float | None) -> str:
    if value is None:
        return "none"
    return f"{value:.3f}s"


def infer_window_size() -> str:
    if sys.stdout.isatty():
        size = shutil.get_terminal_size(fallback=(120, 40))
        return f"{size.columns}x{size.lines}"
    columns = os.environ.get("COLUMNS") or "120"
    lines = os.environ.get("LINES") or "40"
    return f"{columns}x{lines}"


if __name__ == "__main__":
    sys.exit(main())
