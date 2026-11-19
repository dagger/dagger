#!/bin/sh
# PR #15213 "engine: keep contenthash trees on disk".
#
# Every hashed snapshot keeps its whole serialized contenthash tree (~100 B
# per file) in memory via SetExternal, and a derived snapshot (a one-file
# edit of a big directory) has no tree of its own, so it is rescanned and its
# full tree stored. Live memory grows with edits x files.
#
# Workload: in one session, build a FILES-file directory in an exec, then
# take EDITS successive one-file edits of it (withNewFile on an existing
# file) and force a content hash of each (Directory.digest). BATCH edits go
# in one query (they hash concurrently); batches run one after another.
set -eu

FILES=${FILES:-10000}
EDITS=${EDITS:-60}
BATCH=${BATCH:-10}
DIRS=$((FILES / 100))

vars=$(jq -n --rawfile s gen.sh --arg dirs "$DIRS" '{script: ("DIRS=" + $dirs + "\n" + $s)}')

k=0
while [ "$k" -lt "$EDITS" ]; do
  q='query($script: String!) { container { from(address: "alpine:3.20") { withExec(args: ["sh", "-c", $script]) { directory(path: "/d") { base: digest'
  n=0
  while [ "$n" -lt "$BATCH" ] && [ "$k" -lt "$EDITS" ]; do
    q="$q e$k: withNewFile(path: \"d0/f0\", contents: \"edit $k\") { digest }"
    k=$((k + 1))
    n=$((n + 1))
  done
  q="$q } } } } }"
  echo "$q" | dagger api query --no-load-module --var-json "$vars" >/dev/null
  echo "edits: $k/$EDITS"
done
