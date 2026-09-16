#!/bin/sh
set -eu

# The playground's packaged Go bootstraps the version required by hack/otlpdump.
export GOTOOLCHAIN=auto

capture=/tmp/dagger-network-metrics.jsonl
receiver_log=/tmp/dagger-network-metrics-receiver.log
endpoint=http://127.0.0.1:43180
engine_metrics_endpoint=http://dagger-engine:9090/metrics
internal_bytes=$((32 * 1024 * 1024))
external_bytes=$((8 * 1024 * 1024))
demo_tmp=$(mktemp -d)
socket_path=$demo_tmp/demo.sock
operation_report=$demo_tmp/operations.tsv
module=./hack/demo-network-metrics
nonce="$(date +%s)-$$"
socket_pid=
tunnel_pid=

command -v curl >/dev/null
command -v dagger >/dev/null
command -v go >/dev/null
command -v jq >/dev/null
command -v socat >/dev/null
test -f ./go.mod

rm -f "$capture" "$receiver_log"
go build -o /tmp/dagger-otlpdump ./hack/otlpdump
/tmp/dagger-otlpdump \
  -addr 127.0.0.1:43180 \
  -out "$capture" >"$receiver_log" 2>&1 &
receiver_pid=$!

cleanup() {
  if test -n "$tunnel_pid"; then
    kill "$tunnel_pid" 2>/dev/null || true
    wait "$tunnel_pid" 2>/dev/null || true
  fi
  if test -n "$socket_pid"; then
    kill "$socket_pid" 2>/dev/null || true
    wait "$socket_pid" 2>/dev/null || true
  fi
  kill "$receiver_pid" 2>/dev/null || true
  wait "$receiver_pid" 2>/dev/null || true
  rm -rf "$demo_tmp"
}
trap cleanup EXIT

for _ in $(seq 1 100); do
  if curl --silent --output /dev/null "$endpoint/"; then
    break
  fi
  if ! kill -0 "$receiver_pid" 2>/dev/null; then
    sed -n '1,120p' "$receiver_log" >&2
    exit 1
  fi
  sleep 0.1
done
curl --silent --output /dev/null "$endpoint/"
curl --fail --silent "$engine_metrics_endpoint" >"$demo_tmp/network-before.prom"

export OTEL_EXPORTER_OTLP_ENDPOINT=$endpoint
export OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=$endpoint/v1/logs
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=$endpoint/v1/metrics
export OTEL_EXPORTER_OTLP_TRACES_LIVE=1

record_operation() {
  label=$1
  first_line=$2
  sed -n "${first_line},\$p" "$capture" | jq -sr --arg label "$label" '
    [.[]
      | select(.kind == "metric")
      | select(
          .name == "dagger.io/metrics.network.rx.bytes"
          or .name == "dagger.io/metrics.network.tx.bytes"
        )
      | select(.value > 0)
      | {
          direction: (.name | split(".")[-2] | ascii_upcase),
          value: .value
        }]
    | sort_by(.direction, .value)
    | group_by(.direction)
    | map(last)
    | .[]
    | [$label, .direction, .value]
    | @tsv
  ' -r >>"$operation_report"
}

run_operation() {
  label=$1
  shift
  first_line=$(($(wc -l <"$capture") + 1))
  "$@" >/dev/null
  record_operation "$label" "$first_line"
}

run_operation withExec \
  dagger --silent call -m "$module" exec --nonce "$nonce"
run_operation http \
  dagger --silent call -m "$module" http-download --nonce "$nonce"
run_operation git \
  dagger --silent call -m "$module" git-pull
run_operation container.from \
  dagger --silent call -m "$module" container-from
run_operation registry.push \
  dagger --silent call -m "$module" registry-push --nonce "$nonce"

if test -n "${NETWORK_METRICS_LLM_MODEL:-}"; then
  run_operation llm \
    dagger --silent call -m "$module" llm-prompt \
      --model "$NETWORK_METRICS_LLM_MODEL"
fi

mkdir -p "$demo_tmp/input"
head -c 4194304 /dev/urandom >"$demo_tmp/input/payload"
run_operation filesync.import \
  dagger --silent call -m "$module" filesync-import \
    --input "$demo_tmp/input" --nonce "$nonce"
run_operation filesync.export \
  dagger --silent call -m "$module" -o "$demo_tmp/output" \
    filesync-export --nonce "$nonce"

socat UNIX-LISTEN:"$socket_path",fork EXEC:cat &
socket_pid=$!
for _ in $(seq 1 100); do
  test -S "$socket_path" && break
  sleep 0.1
done
test -S "$socket_path"
run_operation host.socket \
  dagger --silent call -m "$module" host-socket \
    --socket "unix://$socket_path" --nonce "$nonce"
kill "$socket_pid" 2>/dev/null || true
wait "$socket_pid" 2>/dev/null || true
socket_pid=

first_line=$(($(wc -l <"$capture") + 1))
dagger --silent call -m "$module" tunnel up --ports 18080:8080 \
  >"$demo_tmp/tunnel.log" 2>&1 &
tunnel_pid=$!
for _ in $(seq 1 300); do
  if curl --fail --silent --output /dev/null \
    http://127.0.0.1:18080/payload; then
    break
  fi
  if ! kill -0 "$tunnel_pid" 2>/dev/null; then
    sed -n '1,120p' "$demo_tmp/tunnel.log" >&2
    exit 1
  fi
  sleep 0.1
done
curl --fail --silent --output /dev/null http://127.0.0.1:18080/payload
sleep 1
kill -INT "$tunnel_pid" 2>/dev/null || true
wait "$tunnel_pid" 2>/dev/null || true
tunnel_pid=
record_operation host.tunnel "$first_line"
curl --fail --silent "$engine_metrics_endpoint" >"$demo_tmp/network-after.prom"

require_metric() {
  label=$1
  direction=$2
  awk -F '\t' -v label="$label" -v direction="$direction" '
    $1 == label && $2 == direction && $3 > 0 { found = 1 }
    END { exit !found }
  ' "$operation_report"
}

require_metric withExec RX
require_metric withExec TX
require_metric http RX
require_metric git RX
require_metric container.from RX
require_metric registry.push TX
require_metric filesync.import RX
require_metric filesync.export TX
require_metric host.socket RX
require_metric host.socket TX
require_metric host.tunnel RX
require_metric host.tunnel TX
if test -n "${NETWORK_METRICS_LLM_MODEL:-}"; then
  require_metric llm RX
  require_metric llm TX
fi

metric() {
  span=$1
  name=$2
  jq -sr --arg span "$span" --arg name "$name" '
    ([.[]
      | select(.kind == "metric")
      | select(.name == $name)
      | select(.attrs["dagger.io/metrics.span"] == $span)]
     | sort_by(.timeNs)
     | last
     | .value) // 0
  ' "$capture"
}

metric_span() {
  name=$1
  minimum=$2
  jq -sr --arg name "$name" --argjson minimum "$minimum" '
    [.[]
      | select(.kind == "metric")
      | select(.name == $name and .value >= $minimum)]
    | sort_by(.timeNs)
    | last
    | .attrs["dagger.io/metrics.span"] // ""
  ' "$capture"
}

mib() {
  awk -v bytes="$1" 'BEGIN { printf "%.2f MiB", bytes / 1048576 }'
}

exact_metric() {
  file=$1
  realm=$2
  scope=$3
  direction=$4
  awk -v realm="$realm" -v scope="$scope" -v direction="$direction" '
    /^dagger_network_bytes_total\{/ &&
      index($0, "realm=\"" realm "\"") &&
      index($0, "scope=\"" scope "\"") &&
      index($0, "direction=\"" direction "\"") {
        printf "%.0f\n", $NF
      }
  ' "$file"
}

exact_delta() {
  realm=$1
  scope=$2
  direction=$3
  before=$(exact_metric "$demo_tmp/network-before.prom" "$realm" "$scope" "$direction")
  after=$(exact_metric "$demo_tmp/network-after.prom" "$realm" "$scope" "$direction")
  echo $((after - before))
}

unattributed_metric() {
  file=$1
  scope=$2
  direction=$3
  awk -v scope="$scope" -v direction="$direction" '
    /^dagger_network_unattributed_bytes_total\{/ &&
      index($0, "scope=\"" scope "\"") &&
      index($0, "direction=\"" direction "\"") {
        printf "%.0f\n", $NF
      }
  ' "$file"
}

unattributed_delta() {
  scope=$1
  direction=$2
  before=$(unattributed_metric "$demo_tmp/network-before.prom" "$scope" "$direction")
  after=$(unattributed_metric "$demo_tmp/network-after.prom" "$scope" "$direction")
  echo $((after - before))
}

network_prefix=dagger.io/metrics.network

client_span=$(metric_span "$network_prefix.internal.rx.bytes" "$internal_bytes")
server_span=$(metric_span "$network_prefix.internal.tx.bytes" "$internal_bytes")
test -n "$client_span"
test -n "$server_span"

client_network_rx=$(metric "$client_span" "$network_prefix.rx.bytes")
client_network_tx=$(metric "$client_span" "$network_prefix.tx.bytes")
client_internal_rx=$(metric "$client_span" "$network_prefix.internal.rx.bytes")
client_internal_tx=$(metric "$client_span" "$network_prefix.internal.tx.bytes")
client_external_rx=$(metric "$client_span" "$network_prefix.external.rx.bytes")
client_external_tx=$(metric "$client_span" "$network_prefix.external.tx.bytes")

server_network_tx=$(metric "$server_span" "$network_prefix.tx.bytes")
server_internal_tx=$(metric "$server_span" "$network_prefix.internal.tx.bytes")
server_external_tx=$(metric "$server_span" "$network_prefix.external.tx.bytes")

if jq -e '
  select(.kind == "metric")
  | select(.name | startswith("dagger.io/metrics.netstat."))
' "$capture" >/dev/null; then
  echo 'unexpected legacy netstat metric' >&2
  exit 1
fi

echo
echo 'PER-OPERATION ESTIMATES'
awk -F '\t' '{ printf "  %-18s %s %7.2f MiB\n", $1, $2, $3 / 1048576 }' \
  "$operation_report"

echo
echo 'AUTHORITATIVE ENGINE CGROUP METRICS'
for realm in userland daggerland; do
  for scope in internal external; do
    for direction in rx tx; do
      bytes=$(exact_delta "$realm" "$scope" "$direction")
      printf '  %-10s %-8s %s %7.2f MiB\n' \
        "$realm" "$scope" "$(echo "$direction" | tr '[:lower:]' '[:upper:]')" \
        "$(awk -v bytes="$bytes" 'BEGIN { print bytes / 1048576 }')"
    done
  done
done

test "$(awk '/^dagger_network_accounting_available / { print $2 }' "$demo_tmp/network-after.prom")" = 1

echo
echo 'UNATTRIBUTED ENGINE CGROUP TRAFFIC'
for scope in internal external; do
  for direction in rx tx; do
    bytes=$(unattributed_delta "$scope" "$direction")
    printf '  %-8s %s %7.2f MiB\n' \
      "$scope" "$(echo "$direction" | tr '[:lower:]' '[:upper:]')" \
      "$(awk -v bytes="$bytes" 'BEGIN { print bytes / 1048576 }')"
  done
done
printf '  realm enforcement: %s\n' \
  "$(awk '/^dagger_network_realm_enforced / { print $2 }' "$demo_tmp/network-after.prom")"
test "$(awk '/^dagger_network_realm_enforced / { print $2 }' "$demo_tmp/network-after.prom")" = 1
test "$(awk '
  /^dagger_network_cgroup_registrations_total\{/ && /result="error"/ {
    print $2
  }
' "$demo_tmp/network-after.prom")" = 0
awk '
  /^dagger_network_cgroup_registrations_total\{/ {
    printf "  cgroup registrations %s: %.0f\n", $1, $2
  }
' "$demo_tmp/network-after.prom"

echo
echo 'PREVIOUS VIEW: reconstructed host-veth aggregate'
printf '  client Network Rx: %s  <- mostly the external upload\n' "$(mib "$client_network_tx")"
printf '  client Network Tx: %s  <- internal and external downloads merged\n' "$(mib "$client_network_rx")"
printf '  server Network Rx: %s  <- data sent to another Dagger container\n' "$(mib "$server_network_tx")"

echo
echo 'NEW VIEW: traffic classified by its remote endpoint'
printf '  client Internal Rx:   %s\n' "$(mib "$client_internal_rx")"
printf '  client Internal Tx:   %s\n' "$(mib "$client_internal_tx")"
printf '  client External Rx:   %s\n' "$(mib "$client_external_rx")"
printf '  client External Tx:   %s\n' "$(mib "$client_external_tx")"
printf '  server Internal Tx:   %s\n' "$(mib "$server_internal_tx")"
printf '  server External Tx:   %s\n' "$(mib "$server_external_tx")"

test "$client_network_rx" -ge $((internal_bytes + external_bytes))
test "$client_network_tx" -ge "$external_bytes"
test "$client_internal_rx" -ge "$internal_bytes"
test "$client_external_rx" -ge "$external_bytes"
test "$client_external_tx" -ge "$external_bytes"
test "$server_network_tx" -ge "$internal_bytes"
test "$server_internal_tx" -ge "$internal_bytes"
test "$server_external_tx" -lt $((internal_bytes / 8))

echo
echo 'PASS: no netstat metrics were emitted and aggregate traffic was split.'
echo "Raw telemetry: $capture"
