#!/bin/sh
# PR #15202 "core: dedupe and bound schema builder memos".
#
# The session's SchemaBuilderMemo keyed the core module by pointer, and every
# asModule wraps core in a new *CoreMod (CoreMod.WithView), so every module
# load built and kept another schema server for its unchanged dependency set,
# until the session ends.
#
# Workload: in one session, load MODS distinct Dang modules (probe/, plus a
# load.txt that differs per load) with Directory.asModule. BATCH loads go in
# one query (one nested client); batches run one after another.
set -eu

MODS=${MODS:-300}
BATCH=${BATCH:-10}

vars=$(jq -n --rawfile cfg probe/dagger.json --rawfile src probe/main.dang '{cfg: $cfg, src: $src}')

k=0
while [ "$k" -lt "$MODS" ]; do
  # shellcheck disable=SC2016 # GraphQL variables, not shell expansions
  q='query($cfg: String!, $src: String!) { directory { withNewFile(path: "dagger.json", contents: $cfg) {'
  n=0
  while [ "$n" -lt "$BATCH" ] && [ "$k" -lt "$MODS" ]; do
    q="$q m$k: withNewFile(path: \"load.txt\", contents: \"load $k\") { withNewFile(path: \"main.dang\", contents: \$src) { asModule { name } } }"
    k=$((k + 1))
    n=$((n + 1))
  done
  q="$q } } }"
  echo "$q" | dagger api query --no-load-module --var-json "$vars" >/dev/null
  echo "modules: $k/$MODS"
done
