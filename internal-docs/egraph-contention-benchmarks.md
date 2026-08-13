# DagQL E-Graph Contention Benchmarks

This suite is an empirical diagnostic for `egraphMu` acquisition frequency,
hold time, and the cache shapes that can make exclusive sections expensive. It
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

Independent, wide-output, and popular-input fixtures have persisted/imported
variants. Imported fixtures are produced by a clean close and reopen through
the current persistence schema, not by mutating in-memory indexes to imitate
import.

The deterministic tests verify dependency collection, exact wide-output class
width, structural lookup fidelity, popular-input fanout, imported posting
broadening, and the absence of imported result/term associations.

## Measurements

The opt-in observer records every instrumented `egraphMu` acquisition as:

- operation label;
- read or write mode;
- acquisition wait;
- lock hold.

It records after unlock so aggregation does not extend the measured hold.
`BenchmarkCacheEGraphInstrumentationOverhead` compares the same warmed exact
hit with lock timing disabled and enabled. The overhead case disables the
separate detail recorder and uses the same one-in-32 lock sampling as the
steady-state benchmark, so this gate measures the timer used for that path.
One-shot and long-writer cases record every acquisition. The output reports
both observed acquisitions and the sampling interval. If the quiet-host median
overhead exceeds 5%, lock timings are considered biased and the run stops for
review. Five disabled/enabled pairs are interleaved to reduce host-drift bias.

The same opt-in observer also records details that lock timing cannot recover:

- lookup and canonicalization candidate counts (the enclosing lock hold records
  their elapsed cost);
- release/prune collection count and peak collector queue depth;
- metadata/disk prune snapshot, planning, and application time, with snapshot
  results, plan/candidate counts, and applied-root counts.

These are emitted as machine-readable `EGRAPH_DETAIL_METRIC` JSON records.
Deterministic tests validate their cardinality semantics; elapsed durations are
reported but never asserted by tests.

Every operation reports the pre-operation cache shape:

- digest width by equivalence class;
- live-result width by output class;
- posting width by digest;
- indexed-digest width by result;
- input-class term fanout;
- total postings;
- associated result count and result/term coverage.

Fixtures with a designated wide output also report that exact target class's
digest and result widths; this avoids confusing it with a slightly wider input
class elsewhere in the same fixture.

Operation benchmarks cover release, exact/extra/structural lookup, direct ID
load, actual `ObjectResult.Receiver`, publication, teaching, metadata prune, and
one small/representative physical disk prune. The steady-state cases run 1 and
up to 24 workers without a long writer. Contention cases synchronize foreground
exact hits with the acquisition of a release, prune-cut, or teaching writer.

## Bounded execution

Use a fresh process per point, three processes per point:

```bash
hack/run-egraph-contention-benchmarks.sh screen /tmp/dagger-egraph-bench
```

The runner saves environment characterization, every raw `go test` and
`/usr/bin/time -v` output, and a TSV manifest. It stops larger points in a
fixture family when:

- setup exceeds 30 seconds;
- the measured operation exceeds 20 seconds;
- Go heap/system memory or observed maximum RSS exceeds 4 GiB;
- the process exceeds the 75-second external safety timeout.

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

The script refuses a second profile in the same output directory.

## Decision gates

1. **Wide removal:** if doubling `R` produces roughly 3-4x release/removal hold
   while independent release remains near-linear, select posting/removal index
   work. At or below roughly 2.5x with sub-10ms holds at `R=2048`, demote it.
2. **Import:** if import broadens postings toward `R*D` and makes exact lookup,
   ID load, Receiver, or removal at least 2x slower, select import-association
   and reconstruction work.
3. **Canonical scans:** if ID/Receiver scales materially with class digest or
   posting width, select a live-result/direct-selection prototype.
4. **Congruence repair:** if merging a singleton scales with the complete `K`
   fanout and becomes material, select loser-side repair work.
5. **Release:** after accounting for wide-class cost, if ordinary independent
   release still causes foreground waits above the provisional 10ms review
   threshold, select quiescent root batching. If only one chain/root remains
   long, root batching is insufficient and deferred/pending reclamation remains
   a separate design decision.
6. **Steady state:** if up to 24 warmed readers contend badly without a long
   writer and exclusive wait dominates, select a narrow exact-recipe read-path
   prototype. If stalls occur only behind writers, reduce writer holds first.
7. **Falsification:** if foreground stalls do not correlate with `egraphMu`
   wait, demote the egraph contention theory before collecting broader data.

The 10ms value is a triage threshold, not a product latency commitment. No
production optimization follows from these benchmarks without a separate
maintainer decision.
