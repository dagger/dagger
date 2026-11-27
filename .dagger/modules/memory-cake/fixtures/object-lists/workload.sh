#!/bin/sh
# A client selecting fields of every element of a large list of plain-data
# objects (SearchResult, and its SearchSubmatch list).
#
# Workload: in one session, build a tree of FILES files (default 100) of
# LINES lines (default 100) in an exec, then run SEARCHES searches (default
# 5) of it with Directory.search for a pattern every line matches once, each
# on a fresh one-file edit of the tree so none is a cache hit. Each search
# selects SELECT (default "filePath lineNumber matchedLines submatches { text
# start end }") of every result: FILES x LINES results per search. Per
# search, the engine's bytes allocated (TotalAlloc) go to /out/searches.txt;
# at the end, the dagql cache's results by field go to
# /out/cache-fields.json.
set -eu

FILES=${FILES:-100}
LINES=${LINES:-100}
SEARCHES=${SEARCHES:-5}
if [ -z "${SELECT:-}" ]; then
  SELECT='filePath lineNumber matchedLines submatches { text start end }'
fi
ENGINE=http://dagger-engine:6060

script="set -eu
mkdir -p /d
awk -v files=$FILES -v lines=$LINES 'BEGIN {
  for (f = 0; f < files; f++) {
    path = \"/d/f\" f \".txt\"
    for (l = 0; l < lines; l++) printf \"line %05d of file %05d: the quick brown fox\\n\", l, f > path
    close(path)
  }
}'"
vars=$(jq -n --arg s "$script" '{script: $s}')
# shellcheck disable=SC2016 # GraphQL variables, not shell expansions
tree='container { from(address: "alpine:3.20") { withExec(args: ["sh", "-c", $script]) { directory(path: "/d")'

echo "query(\$script: String!) { $tree { entries } } } } }" |
  dagger api query --no-load-module --var-json "$vars" >/dev/null

alloc() { curl -sSf --max-time 60 "$ENGINE/debug/vars" | jq -r '.memstats.TotalAlloc'; }

: >/out/searches.txt
n=0
while [ "$n" -lt "$SEARCHES" ]; do
  n=$((n + 1))
  a0=$(alloc)
  results=$(echo "query(\$script: String!) { $tree { withNewFile(path: \".nonce\", contents: \"${CAKE_NONCE:-} $n\") { search(pattern: \"fox\") { $SELECT } } } } } } }" |
    dagger api query --no-load-module --var-json "$vars" |
    jq -e '.container.from.withExec.directory.withNewFile.search | length')
  a1=$(alloc)
  line="search $n results $results alloc $((a1 - a0)) alloc_per_result $(((a1 - a0) / (results > 0 ? results : 1)))"
  echo "$line"
  echo "$line" >>/out/searches.txt
done

curl -sSf --max-time 300 "$ENGINE/debug/dagql/cache" |
  jq '[.results[] | ((.result_call.field // "?") + " -> " + (.type_name // "?") + (if .result_call.nth then " [nth]" else "" end))]
      | group_by(.) | map({field: .[0], n: length}) | sort_by(-.n) | .[:20]' \
    >/out/cache-fields.json || true
