#!/usr/bin/env python3
"""Matched end-to-end filesync versus workspace.snapshot benchmark."""

import argparse
import json
import os
from pathlib import Path
import random
import secrets
import shutil
import statistics
import subprocess
import time


BENCHMARK_CASES = {
    "filesync-with-git": {"snapshot_mode": "filesync", "exclude_git": False},
    "filesync-without-git": {"snapshot_mode": "filesync", "exclude_git": True},
    "git-bundle": {"snapshot_mode": "git-bundle", "exclude_git": True},
}


def workspace_query(case):
    if case not in BENCHMARK_CASES:
        raise ValueError(f"invalid benchmark case {case!r}")
    config = BENCHMARK_CASES[case]
    exclude = ', exclude: [".git"]' if config["exclude_git"] and config["snapshot_mode"] == "filesync" else ""
    return f'''query {{
  currentWorkspace {{
    directory(path: "/", snapshotMode: "{config["snapshot_mode"]}"{exclude}) {{
      digest
      entries
      file(path: "benchmark-probe.txt") {{ contents }}
    }}
  }}
}}\n'''.encode()

PREWARM_QUERY = b'''query { defaultPlatform }\n'''


def run(argv, **kwargs):
    return subprocess.run(
        list(map(str, argv)), check=True, capture_output=True,
        timeout=kwargs.pop("timeout", 600), **kwargs,
    )


def save(path, value):
    Path(path).write_text(json.dumps(value, indent=2) + "\n")


def make_fixture(source, destination):
    base = run(["git", "-C", source, "rev-parse", "origin/main^{commit}"]).stdout.decode().strip()
    run(["git", "clone", "-q", "--local", "--no-hardlinks", "--no-checkout", source, destination])
    run(["git", "-C", destination, "remote", "set-url", "origin", "https://github.com/dagger/dagger.git"])
    run(["git", "-C", destination, "update-ref", "refs/remotes/origin/main", base])
    run(["git", "-C", destination, "checkout", "-q", "-B", "workspace-benchmark", base])
    run(["git", "-C", destination, "branch", "--set-upstream-to", "origin/main", "workspace-benchmark"])

    tracked = run(["git", "-C", destination, "ls-files", "-z"]).stdout.split(b"\0")
    regular = []
    for raw in tracked:
        if not raw:
            continue
        path = destination / os.fsdecode(raw)
        if path.is_file() and not path.is_symlink():
            regular.append(path)
        if len(regular) == 64:
            break
    if len(regular) != 64:
        raise RuntimeError("fixture has fewer than 64 regular tracked files")

    for index, path in enumerate(regular):
        path.write_bytes(random.Random(30_000 + index).randbytes(256 << 10))
    for index in range(128):
        path = destination / "benchmark-untracked" / f"d{index % 16}" / f"f{index}"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(random.Random(40_000 + index).randbytes(128 << 10))
    (destination / "benchmark-probe.txt").write_text("workspace-snapshot-benchmark\n")
    return base


def extract_cli(image, destination):
    image_id = run(["docker", "image", "inspect", image, "--format", "{{.Id}}"]).stdout.decode().strip()
    container = run(["docker", "create", "--entrypoint=/bin/true", image_id]).stdout.decode().strip()
    try:
        run(["docker", "cp", container + ":/usr/local/bin/dagger", destination])
    finally:
        run(["docker", "rm", "-v", container])
    destination.chmod(0o755)
    return image_id


def clean_env(runtime, categories=("CONFIG", "CACHE", "DATA", "STATE")):
    env = {
        key: value for key, value in os.environ.items()
        if not key.startswith(("DAGGER_", "_DAGGER_", "OTEL_"))
    }
    for category in categories:
        env[f"XDG_{category}_HOME"] = str(runtime / "xdg" / category.lower())
    env.update({
        "DO_NOT_TRACK": "1",
        "DNT": "1",
        "DAGGER_NO_NAG": "1",
        "DAGGER_LEAVE_OLD_ENGINE": "1",
    })
    return env


def prepare_cli(spec, destination):
    path = Path(spec)
    executable = shutil.which(spec)
    if path.is_file() or executable:
        source = path if path.is_file() else Path(executable)
        shutil.copy2(source, destination)
        destination.chmod(0o755)
        return f"file:{source.resolve()}"
    return extract_cli(spec, destination)


def run_samples(runtime, fixture, arm, case, topology, cli, env, metadata, timeout):
    samples = []
    for state in ("cold", "warm-unchanged"):
        started = time.perf_counter_ns()
        command = [cli, "--silent"]
        if topology == "cloud":
            command.extend(["--engine", "cloud"])
        command.extend(["-W", fixture, "api", "query", "-M"])
        try:
            result = subprocess.run(
                command, input=workspace_query(case), env=env, cwd=fixture,
                capture_output=True, timeout=timeout,
            )
        except subprocess.TimeoutExpired as err:
            elapsed_ms = (time.perf_counter_ns() - started) / 1e6
            stdout = err.stdout or b""
            stderr = err.stderr or b""
            (runtime / f"{state}.stdout").write_bytes(stdout)
            (runtime / f"{state}.stderr").write_bytes(stderr)
            record = {
                "arm": arm,
                "benchmark_case": case,
                **BENCHMARK_CASES[case],
                "topology": topology,
                "state": state,
                "elapsed_ms": elapsed_ms,
                "exit_code": None,
                "timed_out": True,
                "timeout_seconds": timeout,
                **metadata,
            }
            save(runtime / f"{state}.json", record)
            samples.append(record)
            print(f"{arm} r{record['round']} {state}: >{timeout}s (timed out)", flush=True)
            break
        elapsed_ms = (time.perf_counter_ns() - started) / 1e6
        record = {
            "arm": arm,
            "benchmark_case": case,
            **BENCHMARK_CASES[case],
            "topology": topology,
            "state": state,
            "elapsed_ms": elapsed_ms,
            "exit_code": result.returncode,
            **metadata,
        }
        (runtime / f"{state}.stdout").write_bytes(result.stdout)
        (runtime / f"{state}.stderr").write_bytes(result.stderr)
        if result.returncode:
            raise RuntimeError(f"{arm} {state} failed; see {runtime}")
        data = json.loads(result.stdout)["currentWorkspace"]["directory"]
        record["directory_digest"] = data["digest"]
        record["probe_contents"] = data["file"]["contents"]
        record["git_entry_present"] = any(entry.rstrip("/") == ".git" for entry in data["entries"])
        expected_git_entry = not BENCHMARK_CASES[case]["exclude_git"]
        if record["git_entry_present"] != expected_git_entry:
            raise RuntimeError(
                f"{arm} {state} .git presence was {record['git_entry_present']}, "
                f"expected {expected_git_entry}; see {runtime}"
            )
        save(runtime / f"{state}.json", record)
        samples.append(record)
        print(f"{arm} r{record['round']} {state}: {elapsed_ms:.1f}ms", flush=True)
    return samples


def benchmark_local_arm(root, fixture, round_number, arm, case, cli_spec, image, nonce, timeout):
    runtime = root / f"r{round_number}-{arm}"
    runtime.mkdir()
    cli = runtime / "dagger"
    cli_id = prepare_cli(cli_spec, cli)
    image_id = run(["docker", "image", "inspect", image, "--format", "{{.Id}}"]).stdout.decode().strip()
    engine_name = f"dagger-wsbench-{nonce}-r{round_number}-{arm}"
    env = clean_env(runtime)
    env["DAGGER_ENGINE"] = f"image+docker://{image}?container={engine_name}&volume={engine_name}&cleanup=false"
    try:
        return run_samples(runtime, fixture, arm, case, "local", cli, env, {
            "round": round_number,
            "cli": cli_spec,
            "cli_id": cli_id,
            "image": image,
            "image_id": image_id,
        }, timeout)
    finally:
        inspected = subprocess.run(["docker", "inspect", engine_name], capture_output=True)
        if inspected.returncode == 0:
            subprocess.run(["docker", "logs", engine_name], stdout=(runtime / "engine.log").open("wb"), stderr=subprocess.STDOUT)
            subprocess.run(["docker", "rm", "-f", "-v", engine_name], capture_output=True)
        subprocess.run(["docker", "volume", "rm", "-f", engine_name], capture_output=True)


def benchmark_cloud_arm(root, fixture, round_number, arm, case, cli_spec, image, timeout, prewarm):
    runtime = root / f"r{round_number}-{arm}"
    runtime.mkdir()
    cli = runtime / "dagger"
    cli_id = prepare_cli(cli_spec, cli)
    # Keep the caller's default config location so its Dagger Cloud login is
    # available. Isolate only the stable client ID in XDG state: cache and data
    # do not affect this benchmark.
    env = clean_env(runtime, categories=("STATE",))
    if image:
        env["_EXPERIMENTAL_DAGGER_CLOUD_ENGINE_IMAGE"] = image
    if prewarm:
        started = time.perf_counter_ns()
        result = subprocess.run(
            [cli, "--silent", "--engine", "cloud", "-W", fixture, "api", "query", "-M"],
            input=PREWARM_QUERY, env=env, cwd=fixture,
            capture_output=True, timeout=timeout,
        )
        (runtime / "prewarm.stdout").write_bytes(result.stdout)
        (runtime / "prewarm.stderr").write_bytes(result.stderr)
        if result.returncode:
            raise RuntimeError(f"{arm} Cloud prewarm failed; see {runtime}")
        elapsed = (time.perf_counter_ns() - started) / 1e9
        print(f"{arm} r{round_number} Cloud engine prewarm: {elapsed:.1f}s (not measured)", flush=True)
    return run_samples(runtime, fixture, arm, case, "cloud", cli, env, {
        "round": round_number,
        "cli": cli_spec,
        "cli_id": cli_id,
        "image": image or "cli-default",
    }, timeout)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--timeout", type=int, default=600,
                        help="per-query timeout in seconds; timeouts are recorded and the next arm continues")
    parser.add_argument("--prewarm-cloud", action="store_true",
                        help="start each Cloud engine with a trivial unmeasured query before timing workspace sync")
    parser.add_argument("--arm", action="append", default=[], metavar="NAME=CLI,IMAGE,CASE",
                        help="local arm using native CLI and engine IMAGE")
    parser.add_argument("--cloud-arm", action="append", default=[], metavar="NAME=CLI,IMAGE,CASE",
                        help="Cloud arm using CLI and remote engine IMAGE")
    args = parser.parse_args()
    if args.rounds < 1:
        parser.error("--rounds must be positive")
    if args.timeout < 1:
        parser.error("--timeout must be positive")
    local_arms = []
    for value in args.arm:
        pair = value.split("=", 1)
        if len(pair) != 2:
            parser.error("--arm must be NAME=CLI,IMAGE,CASE")
        config = pair[1].split(",", 2)
        if len(config) != 3:
            parser.error("--arm must be NAME=CLI,IMAGE,CASE")
        case = config[2]
        if case not in BENCHMARK_CASES:
            parser.error(f"invalid benchmark case {case!r}")
        local_arms.append((pair[0], config[0], config[1], case))
    cloud_arms = []
    for value in args.cloud_arm:
        pair = value.split("=", 1)
        if len(pair) != 2:
            parser.error("--cloud-arm must be NAME=CLI,IMAGE,CASE")
        config = pair[1].split(",", 2)
        if len(config) != 3:
            parser.error("--cloud-arm must be NAME=CLI,IMAGE,CASE")
        case = config[2]
        if case not in BENCHMARK_CASES:
            parser.error(f"invalid benchmark case {case!r}")
        cloud_arms.append((pair[0], config[0], config[1], case))
    if not local_arms and not cloud_arms:
        parser.error("at least one --arm or --cloud-arm is required")

    source = args.source.resolve(strict=True)
    root = args.output.absolute()
    root.mkdir(parents=True, exist_ok=False)
    fixture = root / "dagger"
    base = make_fixture(source, fixture)
    nonce = secrets.token_hex(5)
    all_arms = [(name, "local") for name, _, _, _ in local_arms] + [(name, "cloud") for name, _, _, _ in cloud_arms]
    save(root / "run.json", {
        "base": base, "rounds": args.rounds,
        "local_arms": local_arms, "cloud_arms": cloud_arms,
    })

    samples = []
    references = {}
    for round_number in range(1, args.rounds + 1):
        arms = [(name, case, "local", cli, image) for name, cli, image, case in local_arms]
        arms += [(name, case, "cloud", cli, image) for name, cli, image, case in cloud_arms]
        offset = (round_number - 1) % len(arms)
        for arm, case, topology, source, image in arms[offset:] + arms[:offset]:
            if topology == "local":
                rows = benchmark_local_arm(root, fixture, round_number, arm, case, source, image, nonce, args.timeout)
            else:
                rows = benchmark_cloud_arm(
                    root, fixture, round_number, arm, case, source, image,
                    args.timeout, args.prewarm_cloud,
                )
            for row in rows:
                if row.get("timed_out"):
                    continue
                expected = references.setdefault(row["state"], row["probe_contents"])
                if row["probe_contents"] != expected:
                    raise RuntimeError(f"probe differs for {arm} {row['state']}")
            samples.extend(rows)
    save(root / "samples.json", samples)

    lines = [
        "# Workspace snapshot end-to-end benchmark", "",
        "Median CLI wall time over the same fixture. Local cold uses a fresh engine and volume; "
        "Cloud cold uses isolated client state. Warm repeats the unchanged query.", "",
        "| Topology | Case | `.git` included | Cold | Warm unchanged |",
        "| --- | --- | :---: | ---: | ---: |",
    ]
    for arm, topology in all_arms:
        values = {}
        for state in ("cold", "warm-unchanged"):
            rows = [row for row in samples if row["arm"] == arm and row["state"] == state]
            if not rows:
                values[state] = "—"
            elif any(row.get("timed_out") for row in rows):
                values[state] = f">{max(row['timeout_seconds'] for row in rows if row.get('timed_out'))} s"
            else:
                values[state] = f"{statistics.median(row['elapsed_ms'] for row in rows) / 1000:.3f} s"
        completed = [row for row in samples if row["arm"] == arm and not row.get("timed_out")]
        git_included = "yes" if completed and completed[0]["git_entry_present"] else "no"
        lines.append(f"| {topology} | {arm} | {git_included} | {values['cold']} | {values['warm-unchanged']} |")
    (root / "RESULTS.md").write_text("\n".join(lines) + "\n")


if __name__ == "__main__":
    main()
