#!/bin/sh
# PR #15221 "clientdb: intern the log index, pack row metadata".
#
# A session's telemetry store indexes every log row it holds (clientdb
# logLookup) for the session's whole life, once a reader that needs the
# index opens it: the TUI's on-demand logs, a log search, the LLM trace
# tools. On main each row costs a 32 B meta and a row ID, and each span and
# digest that has rows costs string-keyed table and map entries; the PR
# interns span IDs and digests by their bytes and packs the meta into 24 B.
#
# The interning pays per span and per digest, so the shape that shows it is
# many spans with a row or few each -- an agent session's, where every call
# logs its payload under its own span and digest. (One exec printing a
# million lines saves only the 8 B of meta per row.)
#
# Workload: open the main client's index with one log search (what the TUI
# does on its first read), then run CALLS cheap, uncached API calls in
# batches of BATCH aliased selections per query -- each traced, logging its
# call payload under its own span and digest -- and keep the session open.
set -eu

CALLS=${CALLS:-60000}
BATCH=${BATCH:-2000}

# The session's port proxies to the engine as the main client: a log search
# opens its telemetry store with its indexes built, so every row from here
# on is indexed as it arrives.
search() {
  curl -sSf --max-time 300 -u "$DAGGER_SESSION_TOKEN:" \
    -H 'Content-Type: application/json' \
    -d '{"pattern":"memory-cake","limit":1}' \
    "http://127.0.0.1:$DAGGER_SESSION_PORT/v1/logs/search"
  echo
}
search

k=0
while [ "$k" -lt "$CALLS" ]; do
  end=$((k + BATCH))
  [ "$end" -le "$CALLS" ] || end=$CALLS
  awk -v s="$k" -v e="$end" 'BEGIN {
    printf "{"
    for (i = s; i < e; i++)
      printf " c%d: container { withEnvVariable(name: \"K\", value: \"%d\") { envVariable(name: \"K\") } }", i, i
    print " }"
  }' | dagger api query --no-load-module >/dev/null
  k=$end
  echo "calls: $k/$CALLS"
done

# Read once more, so the store has indexed everything stored so far; its
# "next" is the number of log rows.
search
