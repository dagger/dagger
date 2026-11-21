#!/bin/sh
# PR #15232 "engine/snapshots: one ancestry lease for dagql owners".
#
# Every dagql result that owns a snapshot gets an owner lease, and on main
# AttachLease names the snapshot's WHOLE ancestor chain in it (the leases are
# gc.flat). A chain of N results, each a layer on the last, costs ~N*depth/2
# lease resources, all kept in the in-memory containerd metadata store
# (fastbolt), plus the manager's per-snapshot owner maps. Chains flatten at
# 128 layers, so each owner names up to 128 snapshots. The PR names only the
# owner's own snapshot in its lease, and every ancestor once in one shared
# ancestry lease.
#
# Workload: CHAINS directory chains (default 16) built in parallel, each
# DEPTH withNewFile edits deep (default 1000), CHUNK edits per query
# (default 50) with every intermediate result synced, so each one owns its
# snapshot. The chains stay in the open session's cache. 8000 results took
# ~15 s and held ~125 MiB of lease records and owner maps on main.
set -eu

CHAINS=${CHAINS:-16}
DEPTH=${DEPTH:-1000}
CHUNK=${CHUNK:-50}

chain() {
  c=$1
  id=$(echo '{ directory { sync } }' | dagger api query --no-load-module | jq -r .directory.sync)
  i=0
  while [ "$i" -lt "$DEPTH" ]; do
    # shellcheck disable=SC2016 # GraphQL variables, not shell expansions
    q='query($id: ID!) { node(id: $id) { ... on Directory {'
    close=''
    n=0
    while [ "$n" -lt "$CHUNK" ] && [ "$i" -lt "$DEPTH" ]; do
      q="$q withNewFile(path: \"c$c/f$i\", contents: \"chain $c edit $i\") { sync"
      close="$close }"
      i=$((i + 1))
      n=$((n + 1))
    done
    q="$q$close } } }"
    vars=$(jq -n --arg id "$id" '{id: $id}')
    # The deepest sync is the chunk's tip.
    id=$(echo "$q" | dagger api query --no-load-module --var-json "$vars" |
      jq -r '[.. | objects | .sync? // empty] | last')
    [ -n "$id" ] && [ "$id" != null ] || { echo "chain $c: query failed at $i" >&2; exit 1; }
    echo "chain $c: $i/$DEPTH"
  done
}

c=0
pids=''
while [ "$c" -lt "$CHAINS" ]; do
  chain "$c" &
  pids="$pids $!"
  c=$((c + 1))
done
for p in $pids; do
  wait "$p"
done
