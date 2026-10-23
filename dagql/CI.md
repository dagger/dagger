# CI test diagnostics

Test shards use quiet runner output and no goroutine dump watchdog by default.
Verbose logs and goroutine dumps add output and processing overhead, so enable
both only for an investigation:

```sh
dagger api call test-split --diagnostics test-base
dagger api call test-split --diagnostics test-workspaces
```

The `test-split` module's `diagnostics` option applies to every shard. It enables
`otelgotest -v` and dumps the shard's dev engine at 15 and 25 minutes after the
runner starts. Dumps appear in stderr with elapsed-time and engine-URL headers.
The watchdog stops when the runner exits. Omit `--diagnostics` for normal runs.
Use `--engine=container://remote-cache-engine` when targeting the shared local
investigation engine.

To select a small package and set a short timeout:

```sh
dagger api call test-split --diagnostics test-specific \
  --pkg ./engine/telemetry --run '^TestLogBatchProcessorIdleChurnBounded$' --timeout 1s
```

That example intentionally times out. Quiet runner output keeps a timeout's
panic and its list of running tests, followed by the first 64 KiB of the test
goroutine stacks; the package span's logs keep the whole dump. Diagnostics
aren't needed to see them. The engine watchdog is separate: it captures engine
goroutines rather than those of the test process.
