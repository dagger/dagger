#!/bin/sh
# Runs under `dagger run` (see driver.sh): DAGGER_SESSION_PORT/TOKEN name the
# open session, so `dagger` commands in the workload join it. The live
# profile is taken here, before returning, while that session is still open.
#
# In CLI mode (CAKE_MODE=cli) the heap measured is that `dagger run` CLI's,
# our parent: it serves its pretty TUI headlessly on CAKE_CONSOLE and its
# pprof on CAKE_HEAP_URL. The baseline is taken here too, before the
# workload.
set -u
cd /fixture || exit 1
HEAP=${CAKE_HEAP_URL:-http://dagger-engine:6060}
MODE=${CAKE_MODE:-engine}

grab() {
  curl -sSf --retry 5 --retry-delay 2 --retry-connrefused --max-time 300 "$HEAP$1" -o "$2"
}

if [ "$MODE" = cli ]; then
  sleep "$CAKE_SETTLE"
  grab "/debug/pprof/heap?gc=1" /out/baseline.pb.gz
  grab /debug/vars /out/baseline-vars.json
fi

# The workload's own `dagger` commands must not serve a console or pprof of
# their own (the ports are the measured CLI's).
env -u PPROF -u DAGGER_TUI_CONSOLE sh ./workload.sh >/out/workload.log 2>&1
echo $? >/out/workload.exit

if [ "$MODE" = cli ]; then
  # A CLI workload waits, before it returns, until the CLI has ingested its
  # telemetry (it can ask the console, CAKE_CONSOLE); then let it settle.
  sleep "$CAKE_SETTLE"
  curl -sf --max-time 120 "http://$CAKE_CONSOLE/screen" -o /out/live-screen.txt
fi

grab "/debug/pprof/heap?gc=1" /out/live.pb.gz
grab /debug/vars /out/live-vars.json
curl -sSf --max-time 120 http://dagger-engine:6060/debug/client-lifecycle -o /out/live-clients.json
touch /out/session.done
true
