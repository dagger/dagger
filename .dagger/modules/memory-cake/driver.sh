#!/bin/sh
# memory-cake driver: runs in the client container, bound to the engine under
# test as `dagger-engine` (debug server on :6060), with the demo's fixture
# directory at /fixture. Everything it measures lands in /out:
#
#   baseline.pb.gz  heap after a trivial warm-up session (gc=1)
#   live.pb.gz      heap at the end of the workload, its session STILL OPEN
#   live-vars.json  /debug/vars at that point
#   live-clients.json  /debug/client-lifecycle at that point
#   after.pb.gz     heap after the session closed and CAKE_SETTLE seconds
#   after-vars.json /debug/vars at that point
#   <p>.stat        heapstat of each profile (inuse, focus, top focus sites,
#                   and the same for alloc_space)
#   *.log, *.exit   the warm-up, session and workload output and status
#
# Liveness: the workload runs under `dagger run`, which opens a session and
# hands its port and token to the child as DAGGER_SESSION_PORT/TOKEN. Any
# `dagger` command the workload runs (dagger api query, dagger -c, dagger
# call) sees those and connects as a NESTED client of that same session
# instead of opening its own (engine/client.Connect). session.sh takes the
# live profile before returning, so the session is still open when it does.
#
# CLI mode (CAKE_MODE=cli) measures the `dagger run` CLI's heap instead of
# the engine's. That CLI runs the pretty TUI headlessly (DAGGER_TUI_CONSOLE
# serves it over HTTP on CAKE_CONSOLE, rendering frames as a terminal would)
# with its pprof server on (PPROF), so its frontend ingests and renders the
# session's telemetry exactly as an interactive TUI does. session.sh takes
# the baseline (the CLI connected, before the workload) and live profiles
# from it; the after profile is taken once the session has closed, while the
# CLI still holds its TUI (the console keeps serving until interrupted).
set -u

OUT=/out
mkdir -p "$OUT"
MODE=${CAKE_MODE:-engine}

fetch() {
  curl -sSf --retry 5 --retry-delay 2 --retry-connrefused --max-time 300 \
    "http://dagger-engine:6060$1" -o "$2"
}

fail() {
  echo "memory-cake: $1" >&2
  shift
  for f in "$@"; do
    [ -f "$f" ] && { echo "--- tail of $f" >&2; tail -n 40 "$f" >&2; }
  done
  exit 1
}

# Warm up: a trivial session, so the baseline holds what any session leaves.
echo '{ version }' | dagger api query --no-load-module >"$OUT/warmup.log" 2>&1 ||
  fail "warm-up session failed" "$OUT/warmup.log"
sleep "$CAKE_SETTLE"

if [ "$MODE" = cli ]; then
  # This container runs under an outer Dagger session, whose OTLP collector
  # (OTEL_*) and span (TRACEPARENT) the CLI would export everything it
  # receives to: the workload's whole trace would land in the outer session.
  # Interactive users have none of that, so the CLI under test has none.
  for v in $(env | sed -n 's/^\(OTEL_[A-Za-z0-9_]*\)=.*/\1/p'); do
    unset "$v"
  done
  unset TRACEPARENT TRACESTATE BAGGAGE
  start=$(date +%s)
  env DAGGER_TUI_CONSOLE="$CAKE_CONSOLE" PPROF="${CAKE_HEAP_URL#http://}" \
    dagger run sh /cake/session.sh >"$OUT/session.log" 2>&1 &
  cli=$!
  # The CLI serves its console until interrupted, so the session is over
  # when session.sh says so, not when the CLI exits.
  while [ ! -e "$OUT/session.done" ] && kill -0 "$cli" 2>/dev/null; do
    sleep 2
  done
  echo $(($(date +%s) - start)) >"$OUT/session.seconds"
  [ -e "$OUT/session.done" ] || fail "CLI exited before the session finished" "$OUT/session.log" "$OUT/workload.log"
  [ "$(cat "$OUT/workload.exit" 2>/dev/null)" = 0 ] ||
    fail "workload failed (exit $(cat "$OUT/workload.exit" 2>/dev/null))" "$OUT/workload.log" "$OUT/session.log"
  [ -s "$OUT/live.pb.gz" ] || fail "live heap fetch failed" "$OUT/session.log"
  # The command returned: the CLI closes the session but keeps its TUI.
  sleep "$CAKE_SETTLE"
  kill -0 "$cli" 2>/dev/null || fail "CLI exited after the session" "$OUT/session.log"
  curl -sf --max-time 120 "http://$CAKE_CONSOLE/screen" -o "$OUT/after-screen.txt"
  curl -sSf --max-time 300 "$CAKE_HEAP_URL/debug/pprof/heap?gc=1" -o "$OUT/after.pb.gz" ||
    fail "after heap fetch failed" "$OUT/session.log"
  curl -sSf --max-time 120 "$CAKE_HEAP_URL/debug/vars" -o "$OUT/after-vars.json" ||
    fail "after vars fetch failed" "$OUT/session.log"
  kill -INT "$cli"
  i=0
  while kill -0 "$cli" 2>/dev/null && [ "$i" -lt 30 ]; do
    sleep 1
    i=$((i + 1))
  done
  kill -KILL "$cli" 2>/dev/null
  wait "$cli"
  echo $? >"$OUT/session.exit"
else
  fetch "/debug/pprof/heap?gc=1" "$OUT/baseline.pb.gz" || fail "baseline heap fetch failed"
  fetch /debug/vars "$OUT/baseline-vars.json" || fail "baseline vars fetch failed"

  start=$(date +%s)
  dagger run --progress=plain sh /cake/session.sh >"$OUT/session.log" 2>&1
  echo $? >"$OUT/session.exit"
  echo $(($(date +%s) - start)) >"$OUT/session.seconds"
  [ "$(cat "$OUT/workload.exit" 2>/dev/null)" = 0 ] ||
    fail "workload failed (exit $(cat "$OUT/workload.exit" 2>/dev/null))" "$OUT/workload.log" "$OUT/session.log"
  [ -s "$OUT/live.pb.gz" ] || fail "live heap fetch failed" "$OUT/session.log"

  sleep "$CAKE_SETTLE"
  fetch "/debug/pprof/heap?gc=1" "$OUT/after.pb.gz" || fail "after heap fetch failed"
  fetch /debug/vars "$OUT/after-vars.json" || fail "after vars fetch failed"
fi

for p in baseline live after; do
  heapstat -focus "$CAKE_FOCUS" -focus-alloc "${CAKE_FOCUS_ALLOC:-$CAKE_FOCUS}" -top 8 "$OUT/$p.pb.gz" >"$OUT/$p.stat" ||
    fail "heapstat $p failed"
done
