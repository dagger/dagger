#!/bin/sh
# The CLI demos' workload (#15227, #15228, #15229): a long session's worth of
# telemetry for the measuring CLI's frontend to ingest and render.
#
# One exec runs spangen (spangen/ in this module), which streams SPANS spans
# shaped like a long `dagger agent` session (dagql calls with call frames,
# execs, LLM messages, tool calls, tests, checks; exec and test spans log a
# few lines) to the exec's OTLP endpoints, a batch every INTERVAL, and
# alongside it EXPORTS metric exports (SERIES calls x GAUGES gauges, one
# exemplar per point) every METRIC_INTERVAL. The engine forwards all of it to
# the session's clients, so the `dagger run` CLI under test receives it like
# any exec's telemetry. Then wait until that CLI has ingested it: spangen
# ends its root span, spangen-session, last.
set -eu

SPANS=${SPANS:-300000}
BATCH=${BATCH:-512}
INTERVAL=${INTERVAL:-150ms}
EXPORTS=${EXPORTS:-100}
SERIES=${SERIES:-200}
GAUGES=${GAUGES:-8}
METRIC_INTERVAL=${METRIC_INTERVAL:-1s}
LOG_LINES=${LOG_LINES:-3}
DRAIN_TIMEOUT=${DRAIN_TIMEOUT:-900}

# spangen reads its settings from the environment.
dagger -c "container |
  from alpine:3.20 |
  with-env-variable CAKE_NONCE '${CAKE_NONCE:-}' |
  with-env-variable SPANS $SPANS |
  with-env-variable BATCH $BATCH |
  with-env-variable INTERVAL $INTERVAL |
  with-env-variable EXPORTS $EXPORTS |
  with-env-variable SERIES $SERIES |
  with-env-variable GAUGES $GAUGES |
  with-env-variable METRIC_INTERVAL $METRIC_INTERVAL |
  with-env-variable LOG_LINES $LOG_LINES |
  with-file /usr/local/bin/spangen \$(host | file /usr/local/bin/spangen) |
  with-exec spangen |
  stdout"

# The CLI's frontend sees spangen's telemetry after the exec returns.
waited=0
until curl -sf --max-time 120 "http://$CAKE_CONSOLE/spans?q=spangen-session" |
  grep -Eq '^[0-9a-f]+ +ok +spangen-session$'; do
  if [ "$waited" -ge "$DRAIN_TIMEOUT" ]; then
    echo "spangen-session not finished in the CLI after ${waited}s" >&2
    exit 1
  fi
  sleep 5
  waited=$((waited + 5))
done
echo "CLI drained ${waited}s after the exec"
