# Network metrics demo

This demo shows why the original per-exec `Network Rx` and `Network Tx`
metrics were confusing. They came from the host side of the container's veth,
so their apparent direction was reversed, and they merged traffic between
Dagger containers with traffic sent to or received from the Internet.

One measured client exec performs all of the following:

- downloads 32 MiB from another Dagger container;
- downloads 8 MiB from the Internet;
- uploads 8 MiB to the Internet.

The old metrics show only the combined, host-veth totals. The new metrics
classify the same packet stream as `Internal` or `External`, using RX/TX from
the container's point of view. The legacy metrics remain available
for backwards compatibility, so one run demonstrates both views without
needing to build an old Dagger version.

## Prerequisites

Run this from the root of the Dagger repository on Linux 6.6 or newer. The demo
uses TCX eBPF, `jq`, `curl`, Go, and the repository's development CLI and
engine.

Build and start the persistent engine containing the change:

```bash
./hack/build
```

The command also writes the matching development CLI to `./bin/dagger`. The
demo sends 8 MiB in each direction to Cloudflare's public speed-test service,
so do not run it in an environment where that external traffic is undesirable.
Cloudflare documents that the service runs on its network and receives the
client's IP address: <https://speed.cloudflare.com/about>.

## Demo script

Save this as `/tmp/demo-network-metrics.sh`, or paste the entire block into
Bash:

```bash
#!/usr/bin/env bash
set -euo pipefail

# Run from the Dagger repository root.
test -x ./hack/with-dev
test -x ./bin/dagger
command -v curl >/dev/null
command -v go >/dev/null
command -v jq >/dev/null

readonly capture=/tmp/dagger-network-metrics.jsonl
readonly receiver_log=/tmp/dagger-network-metrics-receiver.log
readonly endpoint=http://127.0.0.1:43180
readonly internal_bytes=$((32 * 1024 * 1024))
readonly external_bytes=$((8 * 1024 * 1024))
demo_go=$(mktemp --suffix=.go)

rm -f "$capture" "$receiver_log"
go run ./hack/otlpdump \
  -addr 127.0.0.1:43180 \
  -out "$capture" >"$receiver_log" 2>&1 &
receiver_pid=$!

cleanup() {
  kill "$receiver_pid" 2>/dev/null || true
  wait "$receiver_pid" 2>/dev/null || true
  rm -f "$demo_go"
}
trap cleanup EXIT

# `otlpdump` returns 404 at `/`, but a successful connection proves that the
# receiver is ready. Fail if `go run` exits while it is compiling or starting.
for _ in $(seq 1 100); do
  if curl --silent --output /dev/null "$endpoint/"; then
    break
  fi
  if ! kill -0 "$receiver_pid" 2>/dev/null; then
    cat "$receiver_log" >&2
    exit 1
  fi
  sleep 0.1
done
curl --silent --output /dev/null "$endpoint/"

otel_env=(
  env
  "OTEL_EXPORTER_OTLP_ENDPOINT=$endpoint"
  "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=$endpoint/v1/logs"
  "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=$endpoint/v1/metrics"
  OTEL_EXPORTER_OTLP_TRACES_LIVE=1
)

# `dagger run` gives this small Go client one Dagger session, allowing the
# service object to be bound directly to the client container. A unique
# environment value prevents the measured exec from being served from cache.
# Installing curl happens in an earlier exec, so its APK traffic is not charged
# to the measured exec.
cat >"$demo_go" <<'GO'
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"dagger.io/dagger"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(io.Discard))
	if err != nil {
		return err
	}
	defer client.Close()

	server := client.Container().
		From("python:3.13-alpine").
		WithExec([]string{
			"sh", "-c", "mkdir -p /srv && head -c 33554432 /dev/zero > /srv/payload",
		}).
		WithExposedPort(8080).
		AsService(dagger.ContainerAsServiceOpts{Args: []string{
			"sh", "-c",
			"echo DEMO_NETWORK_SERVER >&2; exec python -m http.server 8080 --directory /srv",
		}})

	_, err = client.Container().
		From("alpine:3.22").
		WithExec([]string{"apk", "add", "--no-cache", "curl"}).
		WithServiceBinding("peer", server).
		WithEnvVariable("DEMO_NONCE", fmt.Sprint(time.Now().UnixNano())).
		WithExec([]string{
			"sh", "-ceu", `
echo DEMO_NETWORK_CLIENT >&2
head -c 8388608 /dev/zero > /tmp/upload
curl --fail --silent --output /dev/null http://peer:8080/payload
curl --fail --silent --output /dev/null 'https://speed.cloudflare.com/__down?bytes=8388608'
curl --fail --silent --output /dev/null --request POST --data-binary @/tmp/upload https://speed.cloudflare.com/__up
`,
		}).
		Sync(ctx)
	return err
}
GO

"${otel_env[@]}" ./hack/with-dev ./bin/dagger --silent run \
  go run "$demo_go"

# The command has returned and flushed telemetry. Locate the public span that
# owns each marker, then its child `exec.run` span, which is what the network
# metric data points reference.
span_id() {
  local marker=$1
  local owner_span
  owner_span=$(jq -r --arg marker "$marker" '
    select(.kind == "span" and ((.name // "") | contains($marker)))
    | .spanId
  ' "$capture" | tail -n1)
  if test -z "$owner_span"; then
    owner_span=$(jq -r --arg marker "$marker" '
      select(.kind == "log" and ((.body // "") | contains($marker)))
      | .spanId
    ' "$capture" | tail -n1)
  fi
  jq -r --arg owner_span "$owner_span" '
    select(.kind == "span")
    | select(.parentId == $owner_span and .name == "exec.run")
    | .spanId
  ' "$capture" | tail -n1
}

client_span=$(span_id DEMO_NETWORK_CLIENT)
server_span=$(span_id DEMO_NETWORK_SERVER)
test -n "$client_span"
test -n "$server_span"

# Return the final value emitted for a metric and exec span.
metric() {
  local span=$1
  local name=$2
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

legacy_prefix=dagger.io/metrics.netstat
network_prefix=dagger.io/metrics.network

client_rx=$(metric "$client_span" "$legacy_prefix.rx.bytes")
client_tx=$(metric "$client_span" "$legacy_prefix.tx.bytes")
client_internal_rx=$(metric "$client_span" "$network_prefix.internal.rx.bytes")
client_internal_tx=$(metric "$client_span" "$network_prefix.internal.tx.bytes")
client_external_rx=$(metric "$client_span" "$network_prefix.external.rx.bytes")
client_external_tx=$(metric "$client_span" "$network_prefix.external.tx.bytes")

server_tx=$(metric "$server_span" "$legacy_prefix.tx.bytes")
server_rx=$(metric "$server_span" "$legacy_prefix.rx.bytes")
server_internal_tx=$(metric "$server_span" "$network_prefix.internal.tx.bytes")
server_external_tx=$(metric "$server_span" "$network_prefix.external.tx.bytes")

mib() {
  awk -v bytes="$1" 'BEGIN { printf "%.2f MiB", bytes / 1048576 }'
}

echo
echo 'OLD VIEW: aggregate metrics (the only network byte metrics previously)'
printf '  client Network Rx: %s  <- mostly the external upload\n' "$(mib "$client_rx")"
printf '  client Network Tx: %s  <- internal + external downloads merged\n' "$(mib "$client_tx")"
printf '  server Network Rx: %s  <- transfer sent to another Dagger container\n' "$(mib "$server_rx")"

echo
echo 'NEW VIEW: the same traffic classified by its remote endpoint'
printf '  client Internal Rx:   %s\n' "$(mib "$client_internal_rx")"
printf '  client Internal Tx:   %s\n' "$(mib "$client_internal_tx")"
printf '  client External Rx:   %s\n' "$(mib "$client_external_rx")"
printf '  client External Tx:   %s\n' "$(mib "$client_external_tx")"
printf '  server Internal Tx:   %s\n' "$(mib "$server_internal_tx")"
printf '  server External Tx:   %s\n' "$(mib "$server_external_tx")"

# Packet headers make the counters slightly larger than the application
# payloads, so assertions use lower bounds. The legacy counters come from the
# host side of the veth, which also reverses their apparent direction: host RX
# is container TX, and host TX is container RX.
test "$client_tx" -ge $((internal_bytes + external_bytes))
test "$client_rx" -ge "$external_bytes"
test "$client_internal_rx" -ge "$internal_bytes"
test "$client_external_rx" -ge "$external_bytes"
test "$client_external_tx" -ge "$external_bytes"
test "$server_rx" -ge "$internal_bytes"
test "$server_internal_tx" -ge "$internal_bytes"
test "$server_external_tx" -lt $((internal_bytes / 8))

echo
echo "PASS: aggregate traffic was split into internal and external traffic."
echo "Raw telemetry: $capture"
```

## What the result demonstrates

Values vary because the counters include packet headers and protocol traffic,
but the output should have this shape:

```text
OLD VIEW: aggregate metrics (the only network byte metrics previously)
  client Network Rx: 8.xx MiB   <- mostly the external upload
  client Network Tx: 40.xx MiB  <- internal + external downloads merged
  server Network Rx: 32.xx MiB  <- transfer sent to another Dagger container

NEW VIEW: the same traffic classified by its remote endpoint
  client Internal Rx:   32.xx MiB
  client Internal Tx:   0.xx MiB
  client External Rx:   8.xx MiB
  client External Tx:   8.xx MiB
  server Internal Tx:   32.xx MiB
  server External Tx:   0.00 MiB
```

The legacy client `Network Tx` value is approximately 40 MiB. Nothing in that
metric says that roughly 32 MiB came from a sibling Dagger container and only
8 MiB came from the Internet. The direction is also from the host veth's point
of view: its TX is the container's RX, and its RX is the container's TX. Thus,
the old server `Network Rx` looks like received data even though the server sent
the 32 MiB payload. All of that payload stayed inside Dagger's CNI network.

The scoped metrics resolve that ambiguity. `Internal Rx/Tx` classifies packets
whose remote address belongs to a Dagger-managed bridge; `External Rx/Tx`
classifies everything not positively identified as Dagger-internal. ARP and
IPv6 link-local control traffic are explicitly internal; malformed and
unsupported traffic is conservatively external.

Because the legacy direction came from the opposite side of the veth, the
following values should be close, allowing for sampling timing and packet
overhead:

```text
legacy Network Tx ~= new Internal Rx + External Rx
legacy Network Rx ~= new Internal Tx + External Tx
```

If the internal metrics stay at zero and all traffic appears external, confirm
that the demo is connected to the engine built from this worktree and that its
kernel supports TCX. When TCX cannot load, attach, or sample, the aggregate is
conservatively classified as external; the implementation intentionally has no
classic TC fallback.
