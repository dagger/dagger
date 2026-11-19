#!/bin/sh
# PRs #15218 "trace tools: filter FindSpans in the store" and #15219
# "FindCalls: search in chunks".
#
# The LLM trace tools search the whole session's telemetry per call. On main
# FindSpans materializes every span to filter it (~81 MB allocated per
# search over 20k spans), and FindCalls renders every call before matching
# (~21 MB peak). Both costs are TRANSIENT: nothing stays allocated after the
# search, so the retained heap shows nothing. This workload measures the
# transient cost itself, from the engine's /debug/vars memstats: TotalAlloc
# per search, and the highest HeapAlloc polled while the searches run (with
# the live heap after a GC just before them). It writes one line per tool to
# /out/peak.txt. TotalAlloc is the robust figure: the polled peak follows GC
# pacing too (GOGC=100 lets garbage reach about the live heap's size).
# ListServices, a trace-free builtin, is the LLM's own overhead.
#
# Workload: CALLS cheap uncached API calls (default 20000), a few spans and
# a call payload each. Then, per tool in TOOLS, SEARCHES searches for QUERY
# one after another, each an LLM whose replayed recording (a recording/
# model: no provider) makes that one tool call. Every LLM's tools include
# the trace tools, and they search the session's main client's telemetry:
# this session's.
set -eu

CALLS=${CALLS:-20000}
BATCH=${BATCH:-2000}
SEARCHES=${SEARCHES:-10}
TOOLS=${TOOLS:-ListServices FindSpans FindCalls}
QUERY=${QUERY:-withEnvVariable}

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

vars() { curl -sSf --max-time 60 http://dagger-engine:6060/debug/vars; }
totalAlloc() { vars | jq '.memstats.TotalAlloc'; }

# search TOOL N: one LLM that calls TOOL once. The replay provider checks
# the history against the recording, so the prompt (unique per search, so no
# search is a cache hit) is in it; once the tool has run there is no next
# message, and the loop's error is expected.
search() {
  args=$(jq -cn --arg tool "$1" --arg q "$QUERY" 'if $tool == "ListServices" then {} else {query: $q, limit: 5} end | tojson')
  recording=$(jq -cn --arg p "search $1 $2" --argjson args "$args" --arg tool "$1" '[
    {role: "USER", content: [{kind: "TEXT", text: $p}]},
    {role: "ASSISTANT", content: [{kind: "TOOL_CALL", callId: "call", toolName: $tool, arguments: $args}]}
  ]' | base64 | tr -d '\n')
  jq -cn --arg m "recording/$recording" --arg p "search $1 $2" '{model: $m, prompt: $p}' >"$tmp/vars.json"
  echo 'query($model: String!, $prompt: String!) { llm(model: $model) { withPrompt(prompt: $prompt) { loop { lastReply } } } }' |
    dagger api query --no-load-module --var-json "$(cat "$tmp/vars.json")" >"$tmp/search.out" 2>&1 || true
}

# Poll HeapAlloc in the background, for the peak.
poll() {
  while [ -f "$tmp/polling" ]; do
    vars | jq '.memstats.HeapAlloc' || true
    sleep 0.05
  done
}

tmp=$(mktemp -d)
: >/out/peak.txt
for tool in $TOOLS; do
  # A warm-up search: the first opens the store and builds its indexes.
  search "$tool" warmup
  tail -n "${SHOW:-5}" "$tmp/search.out"
  # Collect the warm-up's garbage, so the peak is measured from the live
  # heap.
  curl -sSf --max-time 300 "http://dagger-engine:6060/debug/pprof/heap?gc=1" -o /dev/null
  start=$(vars | jq '.memstats.HeapAlloc')
  touch "$tmp/polling"
  poll >"$tmp/heap.$tool" &
  poller=$!
  before=$(totalAlloc)
  n=0
  while [ "$n" -lt "$SEARCHES" ]; do
    search "$tool" "$n"
    n=$((n + 1))
  done
  after=$(totalAlloc)
  rm -f "$tmp/polling"
  wait "$poller" || true
  peak=$(sort -n "$tmp/heap.$tool" | tail -n 1)
  line="$tool searches $SEARCHES alloc_per_search $(((after - before) / SEARCHES)) heap_alloc_start $start peak_heap_alloc $peak"
  echo "$line"
  echo "$line" >>/out/peak.txt
done
