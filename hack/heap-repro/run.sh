#!/usr/bin/env bash

# hack/heap-repro/run.sh rebuilds and restarts the dev engine from one
# worktree, with a fresh state volume, then records the heap timeline of a
# session against it using this checkout's heap-repro tool.
#
#   hack/heap-repro/run.sh <worktree> <label> <out.json> [record flags...]
#
# Run it once per engine build, then render the runs together:
#
#   hack/heap-repro/run.sh ../main main main.json
#   hack/heap-repro/run.sh . client-lifecycle branch.json
#   go run ./hack/heap-repro chart -out heap.html main.json branch.json

set -e -u -o pipefail

if [[ $# -lt 3 ]]; then
    echo "usage: $0 <worktree> <label> <out.json> [record flags...]" >&2
    exit 2
fi

here="$(cd "$(dirname "$(realpath "${BASH_SOURCE[0]}")")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
worktree="$(cd "$1" && pwd)"
label=$2
out="$(realpath -m "$3")"
shift 3

name=${_EXPERIMENTAL_DAGGER_DEV_CONTAINER:-dagger-engine.dev}
echo "resetting $name" >&2
docker rm -fv "$name" >/dev/null 2>&1 || true
docker volume rm "$name" >/dev/null 2>&1 || true

echo "building and starting the engine from $worktree" >&2
(cd "$worktree" && ./hack/dev true)

cd "$repo"
exec "$worktree/hack/with-dev" go run ./hack/heap-repro record -label "$label" -out "$out" "$@"
