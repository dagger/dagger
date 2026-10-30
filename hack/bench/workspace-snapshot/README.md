# Workspace snapshot benchmark

This benchmark compares traditional filesync including `.git`, filesync
excluding `.git`, and the Git-bundle workspace snapshot over both local Docker
and Dagger Cloud. It builds no images itself. Every arm uses the same custom
engine image and matching native CLI.

The fixture is a plain local clone of `dagger/dagger` at `origin/main`, with
64 tracked files replaced by 256 KiB deterministic payloads and 128 untracked
128 KiB files. Each round rotates arm order. Each arm gets an isolated CLI,
fresh engine container, and fresh engine volume. The first query is reported
as `cold`; a second identical query against that engine is `warm-unchanged`.

```console
hack/bench/workspace-snapshot/run.py \
  --source . \
  --output /tmp/workspace-snapshot-e2e \
  --rounds 1 \
  --timeout 300 \
  --arm filesync-with-git=./bin/dagger,localhost/dagger-workspace-candidate:latest,filesync-with-git \
  --arm filesync-without-git=./bin/dagger,localhost/dagger-workspace-candidate:latest,filesync-without-git \
  --arm git-bundle=./bin/dagger,localhost/dagger-workspace-candidate:latest,git-bundle
```

The Cloud arms use the caller's existing Dagger Cloud login. A Cloud arm with
no image override asks its CLI for the default matching engine. An arm with an
override requires a CLI containing the development-only
`_EXPERIMENTAL_DAGGER_CLOUD_ENGINE_IMAGE` hook, as the candidate does.
`--prewarm-cloud` starts each remote engine with a trivial query before timing,
so provisioning is excluded without fetching the workspace base. A timed-out
workspace query is recorded (for example, `>300 s`) and later arms still run.

The query requests the workspace root directory digest, forcing complete
materialization. It verifies that `.git` is present only in the first arm, and
records each resulting directory digest so any other semantic differences stay
visible. Raw stdout, stderr, local engine logs, image/CLI IDs, individual
timings, and one local-versus-Cloud timing table are retained beneath the
output directory.

## Three-way benchmark

`benchmark.sh` runs filesync with `.git`, filesync without `.git`, and Git
bundle from one custom build over one shared fixture. It builds a native CLI
from the current checkout by default, so a Linux engine image also works when
the benchmark runs on macOS. It uses local engines by default:

```console
hack/bench/workspace-snapshot/benchmark.sh \
  --image localhost/dagger-workspace-candidate:latest \
  --rounds 3
```

Use `--cli /path/to/dagger` to provide an already-built matching native CLI.

Pass `--engine cloud` to run the same build and three cases on Dagger Cloud. In
Cloud mode, `--image` must be an image that Cloud can pull:

```console
hack/bench/workspace-snapshot/benchmark.sh \
  --engine cloud \
  --image dagger.namespace-images.com/engine:tibor \
  --rounds 3
```

Run `benchmark.sh --help` to override the image, output directory, or per-query
timeout.
