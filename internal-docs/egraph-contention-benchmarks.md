# DagQL E-Graph Contention Benchmarks

This suite is an empirical diagnostic for `egraphMu` acquisition frequency,
hold time, and cache shapes that can make exclusive sections expensive. It
does not implement or assume a production optimization.

The source of truth is:

- `dagql/cache_egraph_contention_benchmark_test.go`
- `dagql/cache_egraph_benchmark_support_test.go`
- `dagql/cache_measurement.go`
- `hack/run-egraph-contention-benchmarks.sh`

## Fixture contract

The bounded fixtures are:

| Shape | Construction | Approved scales |
| --- | --- | --- |
| Independent | `N` results, unique output classes, no dependencies | 10k, 50k, 200k |
| Chain | One session root owns a length-`N` dependency chain | 1k, 10k, 50k |
| Fanout star | One session root owns `N-1` leaves | 1k, 10k, 50k |
| Shared star | `N-1` session roots own one common dependency | 1k, 10k, 50k |
| Wide output | `R` results share one content class; each has one publication recipe and one taught structural recipe, so the output class has exactly `D=2R+1` digests | 64, 128, 256, 512, 1024, 2048 |
| Wide digest | One result has `D` explicit equivalent digests plus request/response recipes | 1k, 10k |
| Popular input | `K` distinct terms use one class as an input; 1, 8, or 64 singleton classes are merged into it | K=1k, 10k, 50k |

`transient` fixtures are built and measured in one in-memory cache without
persistable results. `persisted-fresh` fixtures build persistable results and
release their setup session without closing the cache. `imported` fixtures do
the same persistable build and then cleanly close and reopen through the
current persistence schema.

The screen intentionally compares transient and imported arms for release,
lookup, ID load/Receiver, and publication/teaching. Those arms differ in both
persistability/lifecycle and import reconstruction, so a delta is associated
with imported state but does not by itself prove that import caused it. A
causal persisted-fresh comparison would require a material matrix expansion.
The smallest useful expansion for the existing wide release and fresh-session
lookup/ID-load routes is 36 points, or 108 additional benchmark processes at
three replicates; it requires separate maintainer approval.

The ownership-prune fixtures are labeled `in-memory-persisted-roots`. They
construct the dependency topology in memory and inject the corresponding
persisted-root ownership edges so metadata-prune traversal sees the intended
chain/star shape. They do not represent a cache reopened from persistence and
must not be compared causally with imported fixture families.

Deterministic tests verify dependency collection, exact wide-output class
width, structural and shared-extra lookup fidelity, popular-input fanout,
imported posting broadening, missing imported result/term associations, and
the cardinality reported for lookup, canonicalization, and prune phases.

## Measurements

The opt-in observer records an instrumented `egraphMu` acquisition as:

- operation label;
- read or write mode;
- acquisition wait;
- lock hold.

It records after unlock so aggregation does not extend the measured hold.
One-shot and contention cases record every acquisition. The steady-state case
uses a deterministic mixed one-in-32 sample. Mixing prevents a fixed modulo
phase from repeatedly selecting only one acquisition in a multi-lock request;
a deterministic test exercises the actual two-acquisition exact-hit path and
requires samples for both `recipe-digest-result-ref/read` and
`lookup-request/write`.

Recorder capacity is based on lock attempts, not logical requests. Every result
reports attempted and recorded acquisition counts plus dropped observations.
Any dropped lock or detail observation fails the benchmark after emitting the
available machine-readable evidence.

Lock summaries and benchmark metrics are grouped by operation and lock mode.
There is deliberately no pooled read/write percentile: decision gates must use
the relevant operation/mode pair, such as `lookup-request/write` wait for the
steady exact path. Raw `EGRAPH_LOCK_METRIC` JSON preserves count, sum, p50,
p95, p99, and maximum wait/hold data for each group.

The same observer records details that lock timing cannot recover:

- lookup and canonicalization candidate counts;
- release/prune collection count and peak collector queue depth;
- metadata and policy-prune snapshot, planning, and application phase time,
  result/candidate counts, and applied-root counts.

These are emitted as `EGRAPH_DETAIL_METRIC` JSON. Deterministic tests validate
their cardinality semantics; elapsed durations are reported but never asserted
by tests. In the full/serial configuration, detail observations are recorded
while the enclosing `egraphMu` critical section is still held, so reported lock
holds include that recorder work. The sampled steady-state configuration turns
detail observations off. The full calibration arm therefore measures this
in-hold cost as part of the instrumentation bias it gates.

Every operation reports the pre-operation cache shape:

- digest width by equivalence class;
- live-result width by output class;
- posting width by digest;
- indexed-digest width by result;
- input-class term fanout;
- total postings;
- associated result count and result/term coverage.

Fixtures with a designated wide output also report that exact target class's
digest and result widths, avoiding confusion with a slightly wider input class
elsewhere in the fixture.

Operation benchmarks cover release, exact/shared-extra/structural lookup,
direct ID load, actual `ObjectResult.Receiver`, publication, teaching, metadata
prune, and a small representative policy prune. The policy-prune case uses
synthetic in-memory size reports and exercises `Cache.Prune`; it does not
measure snapshot deletion or physical disk I/O. Steady-state cases run one and
up to 24 workers without a long writer. Contention cases synchronize foreground
exact hits with acquisition by a release, prune-cut, or teaching writer.

## Instrumentation calibration

Before the screen, five interleaved triples compare the same warmed exact-hit
path on freshly constructed transient wide-output fixtures (`R=64`):

- disabled observation;
- the steady-state configuration (mixed one-in-32 lock timing, details off);
- the serial configuration (every lock timed, details on).

The configuration order rotates between triples to reduce position and warmup
bias; the raw file retains the order and every individual result. All three
arms allocate and keep alive equal-capacity recorder buffers so their live heap
and GC trigger pressure are structurally comparable. Only sampled and full
arms install the observer; recording and timer work remain the intended
differences under measurement.

The gate records every paired overhead, its median, median absolute deviation,
and range for each instrumentation configuration. It stops if either median's
absolute magnitude exceeds 5%, because a large negative estimate is evidence
of an unstable comparison rather than free instrumentation. It also stops as
inconclusive if either median absolute deviation exceeds five percentage
points. Passing only establishes that these two configurations are not visibly
distorting this exact two-acquisition calibration path on the measurement host;
it is not a universal correction factor or proof of sub-5% overhead on every
fixture.

## Bounded execution and raw evidence

Use one fresh process per point, with three processes per point:

```bash
hack/run-egraph-contention-benchmarks.sh screen /tmp/dagger-egraph-bench
```

The runner preserves an append-only environment history, every raw `go test`
and `/usr/bin/time -v` output, the instrumentation-gate inputs and calculation,
and a TSV manifest including preflight commands. The manifest preserves both
the process exit status and a distinct outcome. Per-point outcomes are
`completed`, `benchmark-stop`, `max-rss-stop`, `external-timeout`,
`command-failure`, or `missing-result`; preflight/profile rows also distinguish
`max-rss-failure`. A guard-generated `EGRAPH_BENCH_STOP` is a successful
`benchmark-stop` even when Go emits no benchmark result line; it is not
rewritten as a generic failure.

The runner refuses to overwrite an existing raw result, so a repeated screen
should use a new output directory. A family stop skips its remaining larger
points and then continues the same screen with the next independent family. It
stops larger points in a fixture family when:

- setup exceeds 30 seconds;
- the measured operation exceeds 20 seconds;
- Go heap/system memory or observed maximum RSS exceeds 4 GiB;
- the process exceeds the 75-second external safety timeout.

The complete screen has 216 points and three replicates, plus correctness and
instrumentation preflights: 650 subprocesses. The mechanical external-timeout
ceiling is 13h32m30s, although family stop rules should make an unhealthy run
shorter and ordinary successful processes should be much faster. A selected
contention family adds at most six subprocesses (7m30s timeout ceiling). One
first-anomaly profile adds at most 75 seconds. Thus the full authorized envelope
is 656 subprocesses plus one profile, with a 13h41m15s mechanical ceiling.

Only after a serial anomaly selects a long operation should its contention case
run:

```bash
hack/run-egraph-contention-benchmarks.sh contention /tmp/dagger-egraph-bench release
```

Replace `release` with `prune` or `teach` only when that serial path crossed a
decision gate. Capture CPU, heap, mutex, and block profiles for exactly the
first anomalous fixture:

```bash
hack/run-egraph-contention-benchmarks.sh profile /tmp/dagger-egraph-bench \
  512 '^BenchmarkCacheEGraphRelease/wide-output/imported/512$'
```

The profile uses an atomic in-progress claim and publishes the one-profile
marker only after success. Failed attempts retain their raw output and partial
profiles and can be retried. Block profiling perturbs scheduling and blocking,
so profile timing must not be compared with screen timing; profiles explain a
selected anomaly rather than establish its magnitude.

Contention runs provide only one or up to 24 foreground latency observations
per process. The one-worker result is labeled as a single latency. Multi-worker
p50/p95/max values are descriptive samples, not stable population percentile
estimates.

## Decision gates

1. **Wide removal:** if doubling `R` produces roughly 3-4x
   `release-session/write` hold while independent release remains near-linear,
   select posting/removal index work. At or below roughly 2.5x with sub-10ms
   holds at `R=2048`, demote it.
2. **Imported-state association:** if imported state broadens postings toward
   `R*D` and exact lookup, ID load, Receiver, or removal is at least 2x slower,
   select the 36-point persisted-fresh isolation matrix for approval. Do not
   attribute the delta specifically to import before that isolation.
3. **Canonical scans:** if `id-load-canonical/write` hold and ID/Receiver wall
   time scale materially with class digest or posting width, select a
   live-result/direct-selection prototype.
4. **Congruence repair:** if `teach-content-digest/write` hold for merging a
   singleton scales with the complete `K` fanout and becomes material, select
   loser-side repair work.
5. **Release:** after accounting for wide-class cost, if ordinary independent
   `release-session/write` still causes foreground waits above the provisional
   10ms review threshold, select quiescent root batching. If only one chain/root
   remains long, root batching is insufficient and deferred/pending reclamation
   remains a separate design decision.
6. **Steady state:** if up to 24 warmed readers contend badly without a long
   writer and `lookup-request/write` wait dominates, select a narrow exact-hit
   read-path prototype. If stalls occur only behind writers, reduce writer holds
   first.
7. **Falsification:** if foreground stalls do not correlate with the relevant
   operation/mode `egraphMu` wait, demote the egraph contention theory before
   collecting broader data.

The 10ms value is a triage threshold, not a product latency commitment. No
production optimization follows from these benchmarks without a separate
maintainer decision.
