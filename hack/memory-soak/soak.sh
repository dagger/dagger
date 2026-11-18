#!/bin/sh
# Memory soak: an agent-like workload for comparing engine builds' memory.
#
# Each session calls the soak module (testdata/soak), which, turn after turn,
# grows a Go and a Dang object through calls into their modules (nested
# clients), edits a tree of thousands of files with their state
# (withNewFile, withoutFile) and takes its digest. Sessions are salted, so
# none reuses another's results.
#
# Usage: hack/memory-soak/soak.sh [sessions] [rounds] [files]
#
# Run it against a dev engine, then read the engine's heap after a forced
# GC, e.g. with the engine's debug server on :6060:
#
#   curl -s localhost:6060/debug/gc
#   curl -s localhost:6060/debug/vars | jq '.memstats | {HeapInuse, HeapAlloc}, .schemaBuilds'
#   go tool pprof -top -sample_index=inuse_space http://localhost:6060/debug/pprof/heap | head -25
set -eu

sessions=${1:-6}
rounds=${2:-12}
files=${3:-3000}
here=$(cd "$(dirname "$0")" && pwd)

i=1
while [ "$i" -le "$sessions" ]; do
	dagger call -m "$here/testdata/soak" run \
		--rounds "$rounds" --files "$files" --salt "session-$i-$(date +%s%N)"
	i=$((i + 1))
done
