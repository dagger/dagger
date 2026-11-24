#!/bin/sh
# PR #15212 "engine: forget contenthash trees of collected snapshots".
#
# Every hashed snapshot keeps its whole serialized contenthash tree (~100 B
# per file) in the snapshot manager's in-memory metadata (SetExternal). On
# main nothing drops that metadata when snapshot GC collects the snapshot,
# so the trees of collected snapshots stay in memory for the life of the
# engine. The PR forgets them after every GC that removed snapshots.
#
# Workload: hash EDITS one-file edits of a FILES-file directory, as #15213's
# demo does, but in SEPARATE top-level sessions (the open session's
# variables unset), so nothing holds the results once those sessions end.
# Then prune the local cache: the edits' snapshots are collected, and on
# main their trees stay behind (~355 MiB at the defaults). With the fix
# 36 MiB remains after the session: contenthash's LRU of 20 cacheContexts
# still references the metadata of the last snapshots it hashed. Live, it
# varies from run to run (42-131 MiB): some collected trees are only
# forgotten by a later collection.
set -eu

FILES=${FILES:-20000}
EDITS=${EDITS:-200}
BATCH=${BATCH:-10}
DIRS=$((FILES / 100))

# A query in its own session. The prune needs one too: only a session's
# main client may prune.
own_session() {
  env -u DAGGER_SESSION_PORT -u DAGGER_SESSION_TOKEN dagger api query --no-load-module "$@"
}

vars=$(jq -n --rawfile s gen.sh --arg dirs "$DIRS" '{script: ("DIRS=" + $dirs + "\n" + $s)}')

k=0
while [ "$k" -lt "$EDITS" ]; do
  # shellcheck disable=SC2016 # GraphQL variables, not shell expansions
  q='query($script: String!) { container { from(address: "alpine:3.20") { withExec(args: ["sh", "-c", $script]) { directory(path: "/d") { base: digest'
  n=0
  while [ "$n" -lt "$BATCH" ] && [ "$k" -lt "$EDITS" ]; do
    q="$q e$k: withNewFile(path: \"d0/f0\", contents: \"edit $k\") { digest }"
    k=$((k + 1))
    n=$((n + 1))
  done
  q="$q } } } } }"
  echo "$q" | own_session --var-json "$vars" >/dev/null
  echo "edits: $k/$EDITS"
done

# The local cache entries, summarized, before and after each prune.
stats='{ engine { localCache { entrySet { entryCount diskSpaceBytes entries { activelyUsed recordType } } } } }'
summ='.engine.localCache.entrySet | {entryCount, diskSpaceBytes, active: ([.entries[] | select(.activelyUsed)] | length), types: ([.entries[].recordType] | group_by(.) | map({(.[0]): length}) | add)}'
echo "$stats" | own_session | jq -c "$summ"

# Prune every releasable entry; a prune collects the released snapshots
# before it returns. With the fix, a collection that removed snapshots then
# forgets those already gone from the snapshotter, whose own cleanup can
# lag; so, as a long-lived engine's next GC would, prune again after
# releasing a fresh throwaway snapshot, so that collection removes
# something too and forgets the rest.
for i in 1 2 3; do
  echo '{ engine { localCache { prune } } }' | own_session >/dev/null
  echo "$stats" | own_session | jq -c "$summ"
  sleep 3
  echo "{ directory { withNewFile(path: \"x\", contents: \"${CAKE_NONCE:-x}-$i\") { sync } } }" | own_session >/dev/null
done
