#!/bin/sh
# PR #15215 "core: share module schemas across client roots".
#
# A session built a schema server for a module set once per client root:
# every Go function call is a nested client, and the sets it resolves (the
# module it serves, its dependencies' Deps) were built on its own root and
# kept in the session memo until the session ends. The fix shares one server
# per set across the session's clients, on a neutral root.
#
# Workload: gprobe/ is a Go module whose Greeting calls into a Dang
# dependency (gprobe/probe). One session makes CALLS distinct calls of it
# (different arguments, so none is cached), BATCH aliased calls per query.
set -eu

CALLS=${CALLS:-300}
BATCH=${BATCH:-10}
MOD=${MOD:-gprobe}

k=0
while [ "$k" -lt "$CALLS" ]; do
  q='{'
  n=0
  while [ "$n" -lt "$BATCH" ] && [ "$k" -lt "$CALLS" ]; do
    q="$q c$k: greeting(name: \"call $k\")"
    k=$((k + 1))
    n=$((n + 1))
  done
  q="$q }"
  echo "$q" | dagger -m "./$MOD" api query >/dev/null
  echo "calls: $k/$CALLS"
done
