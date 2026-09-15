---
name: telemetry-capture
description: "Capture and inspect the raw OTel telemetry (spans, logs, metrics) a dagger CLI invocation emits, using the hack/otlpdump tool. Use when debugging telemetry emission, verifying spans/log records/attributes reach the exporter, checking streaming progress records (dagger.io/progress.*), or diagnosing why something doesn't render in the TUI or Dagger Cloud. Triggers on: capture telemetry, OTLP, otlpdump, debug spans, debug telemetry, progress records, telemetry not showing up."
---

# Telemetry Capture

`hack/otlpdump` is a tiny OTLP receiver that dumps every span, log record, and metric a dagger CLI invocation exports to a JSONL file for inspection with `jq`. Use it to see ground truth when the TUI (or Cloud) isn't showing what you expect: the question "was it emitted?" separates emitter bugs from rendering bugs.

## Capture

**Step 1: Start the receiver** (run in background; it serves until killed):

```bash
go run ./hack/otlpdump -addr 127.0.0.1:43180 -out /tmp/telemetry.jsonl
```

**Step 2: Run dagger pointed at it.** The generic endpoint var alone only delivers traces — logs and metrics need their explicit vars too:

```bash
env OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:43180 \
    OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://127.0.0.1:43180/v1/logs \
    OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://127.0.0.1:43180/v1/metrics \
    OTEL_EXPORTER_OTLP_TRACES_LIVE=1 \
    ./hack/with-dev ./bin/dagger ...
```

`OTEL_EXPORTER_OTLP_TRACES_LIVE=1` makes spans export on start as well as end, so you see in-flight state, matching what live consumers receive.

## Inspect

Each JSONL line has a `.kind` of `span`, `log`, or `metric`. Useful queries:

```bash
# all span names
jq -r 'select(.kind=="span") | .name' /tmp/telemetry.jsonl | sort -u

# spans by name prefix, with ids (first 8 hex chars locate children/logs)
jq -r 'select(.kind=="span") | select(.name|startswith("pulling")) | [.spanId[0:8], .name] | @tsv' /tmp/telemetry.jsonl

# streaming progress records (the dagger.io/progress.* convention,
# engine/telemetryattrs/attrs.go), grouped per span
jq -r 'select(.kind=="log" and (.attrs["dagger.io/progress.item"]//empty)!="") |
  [.spanId[0:8], .attrs["dagger.io/progress.item"], .attrs["dagger.io/progress.current"], .attrs["dagger.io/progress.total"]] | @tsv' /tmp/telemetry.jsonl

# log records attached to a specific span
jq -r 'select(.kind=="log" and .spanId[0:8]=="<prefix>") | .body' /tmp/telemetry.jsonl
```

For progress records, the invariants worth checking: records are keyed by (span, item) with latest-wins; emitters throttle (~100ms) but MUST emit a final converged state (`current == total` for known totals); bodies are explicit empty strings (an unset body breaks the OTLP round-trip).

## Gotchas

- The output file is appended across runs — delete it (or use a fresh `-out` path) between captures, and don't delete it out from under a running receiver (it keeps the old fd).
- Cached operations emit no transfer telemetry: cold-state setup matters (e.g. pull a not-yet-pulled image; `./hack/build` fully resets the dev engine cache).
- A missing span in the capture usually means the engine running is stale, not that the code is wrong — verify the dev engine actually contains your change before debugging the emitter.
