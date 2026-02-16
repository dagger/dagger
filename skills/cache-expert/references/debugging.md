# Debugging Cache Issues

This guide focuses on practical debugging for current dagql + filesync cache behavior.

## Core Loop

1. Write down expected identity flow (ID -> cache key -> result ID returned).
2. Log actual values at each boundary.
3. Find first divergence.
4. Decide whether the bug is:
- wrong identity construction
- wrong cache index lookup
- wrong lifecycle/release behavior
- stale compatibility behavior (TTL/session/buildkit integration)

## Repro First

Use a tight test repro before adding logs.

Recommended integration command format:

```bash
dagger --progress=plain call engine-dev test --pkg ./core/integration --run='<TestSuiteName>/<SubtestName>'
```

This command rebuilds the dev engine, runs it as an ephemeral service, and then runs tests against it.
Output includes:
- dev engine build output
- test runner output
- engine logs/printlns
- test logs (e.g. `t.Logf`)

Capture output to a file under `/tmp` to avoid overwhelming terminal context:

```bash
dagger --progress=plain call engine-dev test --pkg ./core/integration --run='<TestSuiteName>/<SubtestName>' > /tmp/cache-debug.log 2>&1
rg -n "panic:|--- FAIL:|^FAIL\s" /tmp/cache-debug.log
```

During long runs, periodically grep for panics. If the engine panics, tests may hang indefinitely:

```bash
rg -n "panic:|fatal error:|SIGSEGV|stack trace" /tmp/cache-debug.log
```

If a test appears hung (engine still alive but no test progress), capture a goroutine dump from the *inner* dev engine process with `SIGQUIT`:

```bash
engine_ctr="$(docker ps --format '{{.Names}}' | rg '^dagger-engine-v' | head -n1)"
docker exec "$engine_ctr" sh -lc '
for p in /proc/[0-9]*; do
  pid=${p#/proc/}
  [ "$pid" = "1" ] && continue
  cmd="$(tr "\0" " " < "$p/cmdline" 2>/dev/null || true)"
  case "$cmd" in
    *"/usr/local/bin/dagger-engine"*)
      echo "sending SIGQUIT to inner dagger-engine pid=$pid" >&2
      kill -QUIT "$pid"
      exit 0
      ;;
  esac
done
echo "no inner dagger-engine process found" >&2
exit 1
'
```

Then inspect the same run log for the dump:

```bash
rg -n "goroutine [0-9]+|fatal error:|SIGQUIT|chan receive|chan send|semacquire|sync\\.Mutex|deadlock" /tmp/cache-debug.log
```

To compare behavior against an engine from another git ref:

```bash
dagger --progress=plain call engine-dev --source 'https://github.com/dagger/dagger#main' test --pkg ./core/integration --run='TestSomeSuite/TestSomeSubtestYouWant'
```

Do not run multiple suites in parallel unless necessary; each suite is CPU-heavy and concurrent runs significantly degrade performance.

Avoid broad `./...` when debugging cache; use focused package/test slices.

`./dagql/idtui` and `./dagql/idtui/multiprefixw` are integration-style test packages (not quick unit loops). Avoid running them during tight cache-debug cycles unless you explicitly need those integration paths.

## Metrics-First Leak Triage

When debugging leaked dagql cache refs, start with Prometheus metrics before adding deep logs.

Enable metrics on the target engine:

```bash
_EXPERIMENTAL_DAGGER_METRICS_ADDR=0.0.0.0:9090
_EXPERIMENTAL_DAGGER_METRICS_CACHE_UPDATE_INTERVAL=1s
```

Key metrics:
- `dagger_connected_clients`
- `dagger_dagql_cache_entries`
- `dagger_dagql_cache_ongoing_calls_entries`
- `dagger_dagql_cache_completed_calls_entries`
- `dagger_dagql_cache_completed_calls_by_content_entries`
- `dagger_dagql_cache_ongoing_arbitrary_entries`
- `dagger_dagql_cache_completed_arbitrary_entries`

Interpretation:
1. If `connected_clients` is `0` but `dagql_cache_entries` stays non-zero, refs are retained.
2. Use bucket metrics to localize leak class:
- `completed_calls` growth: call-result refs not released.
- `ongoing_calls` growth: waiter/cancel path likely stuck.
- `*_arbitrary_*` growth: opaque/arbitrary cache path leak.
3. `dagger_dagql_cache_entries` is index-entry count, not unique-result count.
   The same shared result may appear in multiple indexes.

Practical scrape tip for nested-engine integration tests:
- Prefer scraping via a container bound to the engine service (`curl http://dev-engine:9090/metrics`).
- Scraping from the test process via endpoint hostname may fail DNS resolution in some test networks.

Useful correlation log (session teardown):
- `engine/server/session.go` logs:
  - `released dagql cache refs for session` with `beforeEntries` and `afterEntries`
- If `afterEntries` trends upward across completed sessions, session close is not releasing all refs.

## Where to Log (Most Useful)

### ID + cache key construction

- `dagql/objects.go`
  - `preselect`: log `newID`, returned `cacheCfgResp.CacheKey.ID`, and decoded args after rewrite
  - `newCacheKey`: log `ID`, `DoNotCache`, `TTL`, `ConcurrencyKey`

### Base cache lookup/store

- `dagql/cache.go`
  - `GetOrInitCall`: log `callKey`, `storageKey`, `contentKey`, hit path taken
  - content fallback branch: log source result ID and returned ID override
  - `wait`: log index insertion (`storageKey`, `resultCallKey`, `contentDigestKey`)

### Session wrapper behavior

- `dagql/session_cache.go`
  - forced `DoNotCache` retries (`noCacheNext`)
  - session close races (`isClosed` checks)

### TTL persistence path

- `dagql/cache.go` around DB select/update logic
- `dagql/db/queries.go` compare-and-upsert behavior

### Filesync-specific cache behavior

- `engine/filesync/change_cache.go` for change dedupe/wait/release
- `engine/filesync/localfs.go` for conflict detection (`verifyExpectedChange`) and release timing

## Fast Triage Checklists

### Unexpected miss

Check in order:
1. Did `GetCacheConfig` rewrite ID unexpectedly?
2. Did args decoded from final ID differ from intended runtime args?
3. Did recipe digest change because of view/module/nth/sensitive-arg behavior?
4. Did storage key differ due to TTL/session behavior?
5. If only content should match, does returned ID actually carry content digest?

### Unexpected hit

Check:
1. Which index hit (`storageKey` vs `contentDigestKey`)?
2. Was this an intended content-digest hit?
3. If content hit, was returned ID properly overridden to requested recipe?

### Session-specific oddities

Check:
1. Did prior error set `noCacheNext` for this key?
2. Was call forced to `DoNotCache` and then reinserted?
3. Did session close during execution and release result unexpectedly?

### TTL confusion

Check:
1. Was TTL set for this field?
2. Was result marked safe to persist cache metadata?
3. Was DB metadata updated (or intentionally skipped)?
4. Did expiration force new storage key generation?

## Common Failure Modes

- Cache-config hook rewrites ID but execution args were assumed from old selector args.
- Content-digest reuse works, but returned ID identity is wrong (or assumed wrong).
- Errors trigger forced no-cache retry and hide expected cache behavior on next call.
- Missing releases keep stale entries alive longer than expected.
- Filesync path conflict errors caused by not releasing change-cache refs at sync end.

## Minimal Logging Principle

Prefer small, high-signal log lines with:
- call ID digest
- content digest
- storage key
- hit path (`storage`, `content`, `miss`, `ongoing`)
- whether result ID was overridden

This usually narrows root cause quickly without overwhelming logs.
