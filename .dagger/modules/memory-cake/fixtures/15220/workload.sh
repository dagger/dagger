#!/bin/sh
# PR #15220 "clientdb: intern the span index's span IDs".
#
# A session's telemetry store indexes every span it holds (clientdb
# spanLookup) for the session's whole life, once a reader that needs the
# index opens it: the TUI's on-demand logs, a log search, the LLM trace
# tools. On main the index keeps each span in string-keyed maps (~360-410
# B/span); the PR interns span IDs as uint32s (~135 B/span).
#
# Workload: open the main client's index with one log search (what the TUI
# does on its first read), then run CALLS cheap, uncached API calls in
# batches of BATCH aliased selections per query -- each traced, a few spans
# apiece (a cached call emits none, so every value is distinct) -- and keep
# the session open.
set -eu

CALLS=${CALLS:-60000}
BATCH=${BATCH:-2000}

# The session's port proxies to the engine as the main client: a log search
# opens its telemetry store with its indexes built, so every span from here
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

# Read once more, so the store has indexed everything stored so far.
search
