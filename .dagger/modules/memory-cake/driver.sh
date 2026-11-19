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
#   <p>.stat        heapstat of each profile (inuse, focus, top focus sites)
#   *.log, *.exit   the warm-up, session and workload output and status
#
# Liveness: the workload runs under `dagger run`, which opens a session and
# hands its port and token to the child as DAGGER_SESSION_PORT/TOKEN. Any
# `dagger` command the workload runs (dagger api query, dagger -c, dagger
# call) sees those and connects as a NESTED client of that same session
# instead of opening its own (engine/client.Connect). session.sh takes the
# live profile before returning, so the session is still open when it does.
set -u

OUT=/out
mkdir -p "$OUT"

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

for p in baseline live after; do
  heapstat -focus "$CAKE_FOCUS" -top 8 "$OUT/$p.pb.gz" >"$OUT/$p.stat" ||
    fail "heapstat $p failed"
done
