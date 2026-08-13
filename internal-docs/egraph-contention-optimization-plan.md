# DagQL E-Graph Contention Optimization Plan

Status: approved by Erik for implementation after this plan is recorded in the
repository. This document records the artifact-corrected plan agreed by the
coordinator and the independent Fable 5 reviewer. It does not itself authorize
changes outside the scope below.

## Objective

Reduce the time spent holding the existing global `egraphMu` while results are
removed from the DagQL e-graph. Preserve lookup, equivalence, ownership,
expiry, persistence, release, and pruning semantics.

The first implementation slice has two related changes:

1. Track the exact digest postings created for ordinary runtime results, so
   removal does not scan every digest in an output equivalence class.
2. Maintain an output-equivalence-class-to-results inverse index, so removal
   can determine whether an unexpired result survives without scanning every
   class digest.

Persistence import remains deliberately broad in this slice because the
current schema does not store exact result-to-digest memberships. Imported
results therefore retain a safe class-scan fallback.

## Evidence Basis

### Reviewed implementation and harness

The design was checked against the ownership, release, lookup, e-graph,
persistence import, and prune implementations, including:

- `dagql/cache.go`
- `dagql/cache_egraph.go`
- `dagql/cache_persistence_import.go`
- `dagql/cache_persistence_resolver.go`
- `dagql/cache_persistence_worker.go`
- `engine/server/session.go`
- the persistence schema and generated query paths
- the focused cache, persistence, pruning, compaction, expiry, canonical-race,
  and session-resource tests

The benchmark plan must preserve the reviewed harness unchanged. Its relevant
history is:

- `d8a16ceddb40ceb93b14a59f3e54b3d7eaa80e68`: reviewer-valid harness
  foundation.
- `10b828fad3ff8e41e4c772633266950089474ef2`: clean reduced one-pass
  scaling screen.
- `b39e713b72`: documentation-corrected successor to the reduced screen.
- `61dc7a4e2099b4dc12ef0931ac1cf39e36286755`: forced wide
  metadata-prune fixture after compaction, with both-arm regression coverage.

The implementation branch begins at
`61dc7a4e2099b4dc12ef0931ac1cf39e36286755`. Existing benchmark artifacts and
harness history must remain untouched.

### Collection method

The pre- and post-reboot screens each used 119 fresh `go test` subprocesses:
one correctness preflight, 110 serial fixture points, and eight steady points.
Serial points used full lock observation and detail recording. Steady points
used one-in-32 lock sampling with details disabled. The harness used bounded
setup and measured-operation guards, a 75-second process timeout, and memory
safety checks. Both screens completed all 119 rows with status zero, no
timeouts or resource stops, all references present, and no dropped
observations.

The decisive release measurements use one `egraphMu` acquisition per release.
Full observation adds about 200 ns on the Linux host, which matters for tiny
sub-microsecond operations but cannot explain millisecond or second lock
holds. Steady lock percentiles are sampled and some have few observations.
Profiles are attribution evidence and perturb timing; they are not timing
confirmation.

Preserved artifact roots on the Linux runner are:

- `.egraph-bench-results-61dc7a4e20/`
- `.egraph-bench-results-61dc7a4e20-postreboot/`

The load-bearing summaries include:

- `analysis/point-summary.tsv`
- `analysis/completed-point-metrics.tsv`
- `analysis/lock-metrics.tsv`
- `analysis/detail-metrics.tsv`
- `analysis/practical-comparison.tsv`
- `analysis/practical-scaling-ratios.tsv`
- `followup-analysis/confirmation-observations.tsv`
- `followup-analysis/confirmation-paired-ratios.tsv`
- `followup-analysis/contention-summary.tsv`
- `followup-analysis/contention-locks.tsv`
- the preserved CPU, mutex, block, and heap profile summaries
- both manifests, environment histories, and raw SHA-256 tables

### Established results

#### Transient wide-output release

The transient fixture normally indexes each result under three digests. The
class sizes grow with the result count, but removal currently walks all class
digests for every removed result and then repeats a class-wide survivor scan.

The initial pre-reboot screen measured:

| Scale | Wall | Release write hold | Collected results | Posting memberships |
| ---: | ---: | ---: | ---: | ---: |
| 256 | 16.817 ms | 16.815 ms | 513 | 1,539 |
| 512 | 69.060 ms | 69.058 ms | 1,025 | 3,075 |
| 1,024 | 290.323 ms | 290.321 ms | 2,049 | 6,147 |
| 2,048 | 1,237.425 ms | 1,237.423 ms | 4,097 | 12,291 |

The post-reboot screen measured 15.960, 67.480, 282.505, and 1,162.416 ms at
the same four scales. Two extra fresh-process confirmations per small scale
gave:

- R256: 16.817, 15.725, and 14.748 ms.
- R512: 69.060, 65.682, and 64.239 ms.
- Paired R512/R256 ratios: 4.107, 4.177, and 4.356.

These are artifact-recorded timings and shape counters. This plan does not
claim an unrecorded exact count of internal digest-loop iterations.

#### Persisted-fresh metadata prune

Persisted-fresh pruning repeats the same scaling after reboot. Prune plans
off-lock and takes one write lock per persisted-root cut, so its summed cut
holds are not one continuous stall.

| Scale | Pre wall / cut-hold sum | Post wall / cut-hold sum |
| ---: | ---: | ---: |
| 256 | 15.557 / 14.361 ms | 14.838 / 14.253 ms |
| 512 | 65.870 / 64.591 ms | 64.927 / 63.777 ms |
| 1,024 | 278.800 / 275.756 ms | 269.061 / 266.180 ms |
| 2,048 | 1,126.028 / 1,119.443 ms | 1,097.225 / 1,092.566 ms |

This means collection-call batching alone is insufficient: prune invokes
small cascades once per root and would continue to rescan a wide class.

#### Imported wide-output state

Imported state is structurally different. Import reconstructs each result
under every digest in its output equivalence class because the current schema
does not persist exact result-to-digest membership or imported term-result
associations.

| Scale | Pre wall | Post wall | Real posting memberships |
| ---: | ---: | ---: | ---: |
| 256 | 47.030 ms | 45.339 ms | 263,683 |
| 512 | 209.016 ms | 195.712 ms | 1,051,651 |
| 1,024 | 899.121 ms | 866.394 ms | 4,200,451 |
| 2,048 | 4,169.941 ms | 3,796.078 ms | 16,789,507 |

At R2048, an imported result is indexed under approximately 4,099 digests.
The pre-reboot release allocated about 2.87 GB across 179 million allocations.
Exact runtime reverse membership cannot eliminate these imported removals:
the memberships are real under the current import representation.

Imported metadata prune remains similarly broad. At post-reboot R2048 it used
4,097 cut acquisitions, with a maximum individual hold of 4.62 ms and a summed
hold of 4.834 seconds.

#### Contention consequence

The direct contention follow-up used a 50,000-result chain cascade, not an
independent-root fixture:

| Foreground workers | Release hold | Foreground result | Lookup write wait |
| ---: | ---: | ---: | ---: |
| 1 | 70.927 ms | 70.884 ms | 70.853 ms |
| 12 | 74.122 ms | p50 74.109 ms, p95 74.190 ms | p50 74.095 ms, p95 74.106 ms |

The 12-worker case has only twelve foreground samples and is descriptive. The
one-worker case nevertheless demonstrates directly that a long global writer
becomes foreground exact-hit latency almost one-for-one.

The transient R512 CPU profile placed 60 of 80 ms of cumulative samples in
`ReleaseSession -> collectUnownedResultsLocked ->
removeResultFromEgraphLocked`. Digest removal and deterministic survivor
selection each had about 30 ms cumulative. The mutex profile contained only
about 721 microseconds and was runtime-dominated; the block profile was 99.31%
harness channels. Use the CPU profile to locate work, not to quantify savings
or independently prove contention.

#### Controls and adjacent hotspots

The independent transient 50,000-result control measured 113.520 ms
pre-reboot and 121.157 ms post-reboot. At the largest points, independent
release was about 0.57-0.62 seconds at 200,000 results, chain release about
0.072-0.086 seconds at 50,000, star release about 0.114-0.128 seconds at
50,000, and wide-digest release about 1 ms at width 10,000. These controls are
not perfectly linear, but they do not reproduce the stable wide-output
roughly-fourfold doubling.

Imported read paths are also pathological and remain outside this slice. At
R2048, structural lookup was about 697-753 ms, direct ID load about 722-775 ms,
and Receiver load about 719-733 ms. Popular-input congruence repair is another
credible direction: 64 transient merges into K=50,000 took about 510-626 ms.
Neither is automatically part of the removal fix.

### Limitations and non-conclusions

- The screens contain single observations per point. The decisive small-scale
  slope was confirmed with two additional processes at each scale.
- The shapes are synthetic. The evidence establishes mechanism and practical
  consequence, not production prevalence or total product impact.
- Transient versus imported is an association, not a controlled causal
  attribution. Source inspection establishes the broad import reconstruction
  mechanism directly.
- Full-observation overhead matters for tiny operations but not the measured
  millisecond and second release holds.
- Steady percentiles are sampled; small-sample p95 values can move.
- Profiles are coarse attribution evidence, not magnitude confirmation.
- The current evidence does not justify lock sharding or a core semantic
  redesign.
- The first slice is expected to make transient and persisted-fresh removal
  substantially cheaper. It is not expected to solve imported broad removal
  or imported read paths.

## Scope

### In scope

1. Add an inverse result/output-class index and use it for unexpired survivor
   discovery.
2. Add exact per-result digest-posting bookkeeping for runtime indexing.
3. Mark imported results broad and retain class scanning for their imported
   postings.
4. Maintain all new derived state through association, merge, compaction,
   import, removal, rollback/collection, and reset.
5. Add focused correctness, persistence, pruning, concurrency, and benchmark
   validation.
6. Document the new in-memory ownership of these indexes and the current
   persistence limitation.

### Explicit non-goals

- No `egraphMu` sharding, lock-mode redesign, or read/write split.
- No tombstones, asynchronous cleanup, batching, deferred collection, or
  release/prune visibility change.
- No change to incoming ownership counts, dependency ownership, persisted
  edges, session ownership, canonical-result handoff, cascade ordering, or
  `OnRelease` callback timing.
- No change to TTL, expiry filtering, expired-result collection, term
  provenance, equivalence, congruence repair, or lookup candidate semantics.
- No persistence schema/version change or database migration.
- No exact imported digest-membership persistence in this slice.
- No imported read-path or popular-input repair optimization.
- No benchmark harness, debug endpoint, trace schema, public API, or feature
  flag change.
- No attempt to recalibrate the coarse cache metadata estimate without memory
  evidence.
- No full benchmark screen during implementation iteration.

## Data Structures and Lifetime

Add three `egraphMu`-protected fields to `Cache`, beside the existing e-graph
indexes:

```go
resultIndexedDigests  map[sharedResultID][]string
broadlyIndexedResults map[sharedResultID]struct{}
outputEqClassResults  map[eqClassID]map[sharedResultID]struct{}
```

### `resultIndexedDigests`

This is the exact reverse list of digest postings created by ordinary runtime
indexing. A digest appears at most once per result. The removal path may delete
these postings directly without consulting the result's output class.

It is a slice rather than a per-result set because runtime results normally
have a small number of postings. Deduplication happens at insertion by using
the boolean returned by `TreeSet.Insert`.

### `broadlyIndexedResults`

This marks results whose exact reverse list is incomplete. Removal of a marked
result must additionally scan all digests in its affected output classes. In
the first slice, persistence import is the only production source of broad
results.

Do not duplicate imported class-wide memberships into
`resultIndexedDigests`; doing so would create another multi-million-entry
index in precisely the pathological state being contained.

An imported result may later acquire exact runtime postings. Those new exact
postings may be recorded, but the broad marker remains authoritative until
the result is removed.

### `outputEqClassResults`

This is the inverse of `resultOutputEqClasses`, keyed by current equivalence
class roots. It answers which results are associated with an output class
without re-deriving the answer by scanning every class digest and posting set.

### Lifetime and metadata estimate

Keep all three maps on `Cache`, not `sharedResult`. A shared result wrapper can
outlive one cache e-graph reset, while derived indexes must have exactly the
same lifetime as `resultsByID` and the other e-graph indexes.

Initialize all three in `initEgraphLocked`. Nil all three in
`maybeResetEgraphLocked` together with the current indexes and counters.

The metadata estimate remains the existing fixed formula based on result,
term, and class-slot counts. It does not enumerate individual maps. Normal
runtime bookkeeping is a small digest-header slice plus association entries
of the kind the calibrated per-result coefficient is intended to absorb;
broad imports add one marker without reverse strings. Do not claim new
precision or change the formula in this slice. Revisit only if later memory
evidence shows material drift.

## Digest Posting Helper and Routing Rules

Introduce a small internal posting-kind enum for exact and broad insertions,
plus one central helper such as:

```go
func (c *Cache) addResultDigestPostingLocked(
	resID sharedResultID,
	digest string,
	kind resultDigestPostingKind,
)
```

The helper requires `egraphMu` and initialized maps. Its contract is:

1. Ignore zero result IDs and empty digests.
2. Create or find `egraphResultsByDigest[digest]`.
3. Insert the result ID into that `TreeSet`.
4. For an exact insertion, append the digest to
   `resultIndexedDigests[resID]` only when `Insert` returned true.
5. For a broad insertion, do not append a reverse string.
6. Never treat an untracked broad insertion as exact.

Add a separate `markResultBroadlyIndexedLocked(resID)` helper or equivalent.
Persistence import must mark each imported result once before its class-digest
loop, rather than performing a redundant broad-marker write for every
posting.

Route every production insertion into `egraphResultsByDigest` through the
central helper:

- request recipe digests;
- response recipe digests;
- request and response extra digests;
- teaching and re-indexing through `indexResultDigestsLocked`;
- every class-wide posting reconstructed by persistence import.

Use `rg` during implementation to enumerate all mutations. After the change,
no non-test production path may insert directly into
`egraphResultsByDigest`.

The import sequence is:

1. Reconstruct and register the result.
2. Reconstruct its result/output-class associations through the paired helper
   described below.
3. Mark the result broad once if class-wide postings will be added.
4. Insert those postings through the central helper in broad mode.

If teaching later inserts a new exact digest for a broad result, record it only
when it is a new posting. Broad removal remains required because the imported
postings are still not represented by the exact list.

## Forward and Inverse Output Associations

Add paired helpers so the two directions cannot drift:

```go
func (c *Cache) addResultOutputEqClassLocked(
	resID sharedResultID,
	outputEqID eqClassID,
)

func (c *Cache) removeResultOutputEqClassesLocked(
	resID sharedResultID,
)
```

### Add

`addResultOutputEqClassLocked` must:

1. Reject zero IDs.
2. Canonicalize `outputEqID` with `findEqClassLocked`.
3. Add the root to `resultOutputEqClasses[resID]` idempotently.
4. Add the result to `outputEqClassResults[root]` idempotently.

Use it from `associateResultWithTermLocked` and persistence import. Production
code must not write only one side.

### Remove

`removeResultOutputEqClassesLocked` must:

1. Iterate the result's current forward roots.
2. Remove the result from each inverse set.
3. Delete empty inverse sets.
4. Delete the result's forward entry.

Capture the affected output roots before calling this helper, because term
cleanup still needs them after the association has been removed.

### Equivalence merge

In `mergeEqClassesNoRepairLocked`, after union-by-rank selects the winning and
losing roots:

1. Merge `outputEqClassResults[losingRoot]` into the winner.
2. For each moved result, delete the losing root from its forward set and add
   the winner.
3. Treat a result already present in both roots idempotently: after the merge
   it has one forward entry for that merged component.
4. Delete the losing inverse set.

Do not scan all results. This maintenance is proportional to the losing
inverse set and follows the same merge path for recursive congruence repair.

After this change, production-created forward sets contain current roots.
Keep the existing `findEqClassLocked` normalization in
`outputEqClassesForResultLocked` as cheap defensive behavior. Persistence
snapshotting already uses that normalized reader, so eager forward rewriting
does not change persisted semantics.

### Compaction

`compactEqClassesLocked` already rebuilds `resultOutputEqClasses` under
remapped roots. During that same pass:

1. Build a new `outputEqClassResults` from the rebuilt forward map.
2. Assign the new forward and inverse maps together after all remapping is
   complete.

Exact digest strings and broad markers contain no class IDs and survive
compaction unchanged.

## Removal Algorithms

### Exact and broad digest cleanup

Replace `removeResultDigestsLocked` with logic that receives the result ID and
the captured affected output roots:

1. Read `resultIndexedDigests[resID]`.
2. For each listed digest, remove `resID` from
   `egraphResultsByDigest[digest]`.
3. Delete an empty posting set.
4. If `resID` is broad, additionally perform the existing scan across every
   digest in each affected canonical output class and remove it there.
5. Delete `resultIndexedDigests[resID]` and
   `broadlyIndexedResults[resID]` after cleanup.

Exact cleanup is idempotent. A broad result can have a partial exact list;
duplicates between the exact path and broad scan are harmless.

Runtime transient and persisted-fresh results must not take the class scan.
Imported results must continue to take it until the persistence format can
reconstruct exact membership.

### Survivor discovery

Replace `firstResultForOutputEqClassDeterministicallyAtLocked` with an
existence check:

```go
func (c *Cache) hasUnexpiredResultForOutputEqClassLocked(
	outputEqID eqClassID,
	nowUnix int64,
) bool
```

The helper must:

1. Canonicalize the class root.
2. Iterate `outputEqClassResults[root]`.
3. Load each result from `resultsByID`.
4. Skip nil and expired results.
5. Return immediately on the first live, unexpired result.

Do not replace this with `len(set) > 0`. Current behavior treats a class whose
only remaining results are expired as empty and cleans its terms. The new
helper must preserve that behavior.

Use one `nowUnix` captured at the same point as the current removal path for
all affected classes. Do not opportunistically remove expired results or
otherwise change ownership and expiry cleanup timing. A class with many
expired members can still require a linear scan; that preserves semantics and
is not the measured common case.

The current deterministic helper's lowest-ID choice is not externally
observable in this caller because removal uses only nil versus non-nil. Source
inspection confirms that the outer helper is called only by removal and
`firstResultDeterministicallyAtLocked` is called only by that outer helper.
Delete both after replacing the caller.

### Removal sequence

Preserve the current mutation and trace ordering. Only the digest traversal
and survivor-check internals change:

1. Capture affected output roots from the forward association map.
2. Remove result/term links and emit the same association traces.
3. Remove digest postings through the exact/broad algorithm.
4. Remove forward and inverse output associations through the paired helper.
5. Clear the stored result call, remove the result from `resultsByID`, and
   emit the existing result trace in the existing order.
6. Capture one `nowUnix` and use the inverse unexpired check for each affected
   class before cleaning terms.
7. Perform term and class cleanup exactly as today.
8. Call `maybeResetEgraphLocked` exactly as today.

Preserve the existing early return:

```go
if len(c.egraphTerms) == 0 || len(c.resultOutputEqClasses) == 0 {
	c.maybeResetEgraphLocked()
	return
}
```

There is a pre-existing theoretically inconsistent state where terms are
nonempty but result/output associations are empty. `maybeResetEgraphLocked`
does not reset that state, and the early return could leave a result behind.
Production invariants should make it unreachable. Do not change that behavior
or add a production panic in this performance slice. The test-only
consistency checker should detect the inconsistency at stable checkpoints.

## Required Invariants

Under `egraphMu`, preserve all of the following:

1. Every `resultOutputEqClasses[resID][root]` has
   `outputEqClassResults[find(root)][resID]`.
2. Every inverse output-class member has the corresponding normalized forward
   association.
3. Forward association sets contain current roots and no duplicate entry for
   the same merged component.
4. Every digest in `resultIndexedDigests[resID]` has an
   `egraphResultsByDigest[digest]` posting containing `resID`.
5. A result's exact reverse list contains no duplicate digest string.
6. Every production-created digest posting is either exact-listed for that
   digest or its result is in `broadlyIndexedResults`. A result may be both
   broad and have some exact entries.
7. Every result ID referenced by `resultIndexedDigests`,
   `broadlyIndexedResults`, or `outputEqClassResults` exists in `resultsByID`.
8. Import marks every class-wide posting broad, so a schema-compatible import
   cannot leave an untracked posting.
9. Merge and compaction maintain both association directions at current
   roots.
10. Direct and structural lookup observe exactly the same posting sets while
    a result is live. This slice changes insertion bookkeeping and deletion
    traversal, not lookup membership.
11. `resultsByID` lifetime and incoming ownership accounting remain unchanged,
    including session edges, persisted edges, dependency edges, and
    original/canonical publication handoffs.
12. Release cascade queueing, callback ordering, term provenance, TTL, expiry
    filtering, persisted snapshot behavior, pruning cuts, and reset semantics
    remain unchanged.

## White-Box Test Contract Narrowing

Some existing tests mutate internal maps directly. The new bookkeeping makes
the production contract explicit: removal guarantees cleanup for postings
created through production helpers and for broad imported postings. It does
not guarantee cleanup of arbitrary test-only postings inserted behind those
helpers.

### Foreign-digest release test

Rewrite `TestCacheReleaseRemovesDigestPostingsFromEntireOutputEqClass`:

1. Retain the real cache setup and merge of a foreign digest into the result's
   output class.
2. Add the foreign result posting through the new exact posting helper rather
   than assigning `egraphResultsByDigest` directly.
3. Rename the test to describe the supported behavior, for example
   `TestCacheReleaseRemovesRecordedDigestPostingAfterOutputClassMerge`.
4. Retain the assertion that release removes the production-recorded posting
   while preserving the independently owned keeper result.
5. State in the test that the contract covers recorded exact postings and
   broad imported postings, not arbitrary white-box mutations.

This is a deliberate narrowing of an unsupported internal test contract, not
a product semantic change.

### Other direct fixtures

- In `TestDirectDigestLookupHitsWithoutTermIndex`, the test deliberately
  clears term and result/output association state while retaining direct
  postings. Clear `outputEqClassResults` together with
  `resultOutputEqClasses` so both directions remain consistent.
- In `TestCompactEqClassesSkipsWhenBelowThreshold`, construct associations
  through `addResultOutputEqClassLocked`.
- In
  `TestCachePruneDoesNotProtectTermProvenanceOnlyResultFromActiveResult`,
  construct associations through the paired helper.
- Use `rg` to find every remaining direct write to
  `resultOutputEqClasses` or `egraphResultsByDigest`. Convert it to the new
  helper when the fixture intends a production-valid state. If a test
  deliberately models corruption, make that purpose explicit and do not run
  the ordinary consistency checker on the corrupt intermediate state.

## Test Plan

### Test-only consistency helper

Add a package-internal test helper, not a production API. It must run only at
stable checkpoints while holding `egraphMu` and report concrete result IDs,
digests, and class IDs on failure.

It checks:

1. Forward association to normalized inverse.
2. Inverse association to normalized forward.
3. Exact reverse lists have no duplicates and every listed posting contains
   the result.
4. Every digest posting is exact-listed for that digest or its result is
   broad.
5. Every exact-map key, broad marker, and inverse member exists in
   `resultsByID`.

### Focused behavior tests

Add or extend focused tests for:

1. **Exact transient posting removal.** Create recipe and extra postings,
   attempt duplicate insertion, release the result, and verify every posting
   and reverse entry is removed.
2. **Broad imported removal.** Round-trip through persistence/import, verify
   the result is broad without a huge exact reverse list, release or prune it,
   and verify every class-wide posting and marker is removed.
3. **Mixed survivor.** Put two associated results in one output class. Remove
   one and verify terms, inverse membership, and the unexpired survivor remain.
4. **Expired-only class.** Leave only an expired result and verify it does not
   count as a survivor and terms are cleaned as before.
5. **Merge then removal.** Merge output classes containing results on both
   sides. Verify eager normalized forward/inverse state, including a result
   associated with both pre-merge roots, and then remove each result.
6. **Forced compaction.** Force class compaction and verify both association
   directions are rebuilt under remapped roots; exact and broad digest cleanup
   must still work afterward.
7. **Reset.** Remove the final result and verify all three new maps are nil
   with the existing e-graph state and counters.
8. **Release cascade.** Release a multi-result dependency closure and verify
   incoming ownership counts, callbacks, and derived-index cleanup.
9. **Prune.** Prune persisted-fresh roots one at a time and verify result,
   term, posting, and derived indexes are empty at completion.
10. **Persistence compatibility.** Reopen an existing-schema store, verify
    unchanged lookup behavior, broad imported state, and complete cleanup
    without a migration.
11. **Rollback and concurrency.** Add consistency assertions to
    `TestCacheCanonicalEquivalentSwapRacesSessionRelease` after the canonical
    swap/release collector path and to
    `TestCachePersistenceImportedObjectHitWithoutServerErrors` after its
    load/decode-error session-ownership rollback.
12. **Direct-map fixtures.** Run the renamed foreign-digest test,
    `TestDirectDigestLookupHitsWithoutTermIndex`,
    `TestCompactEqClassesSkipsWhenBelowThreshold`, and
    `TestCachePruneDoesNotProtectTermProvenanceOnlyResultFromActiveResult`
    after converting their setup.

Tests assert behavior and index invariants, never wall-clock timing.

## Implementation Sequence

Implement as two independently reviewable commits in one scoped future
implementation branch or pull request.

### Commit 1: `dagql: index results by output equivalence class`

- Add `outputEqClassResults`.
- Add paired association/removal helpers.
- Maintain both directions through association, import, merge, compaction,
  removal, and reset.
- Replace class-digest survivor discovery with the inverse unexpired existence
  check.
- Delete the two now-unused deterministic result-selection helpers.
- Add focused tests and update the e-graph documentation for this slice.

### Commit 2: `dagql: track exact result digest postings`

- Add `resultIndexedDigests` and `broadlyIndexedResults`.
- Add central posting insertion, broad-marking, and removal helpers.
- Route every runtime and import posting write.
- Retain broad import fallback.
- Rewrite direct-map fixtures and add exact/broad cleanup tests.
- Update persistence documentation.

Either feature should be conceptually revertible without retaining a semantic
change: reverting exact posting tracking restores broad class-scan removal
while retaining inverse survivor discovery; reverting inverse discovery
restores the old survivor path while exact posting removal remains possible.
If source coupling makes literal arbitrary-order reverts unsafe, keep the
functional separation and document the required revert order rather than
adding an abstraction layer solely for reverts.

## Bounded Local Validation

Do not run broad repository tests.

1. Run `gofmt` on changed Go files.
2. Run an anchored `go test ./dagql -run ...` alternation containing all new
   derived-index tests and the modified release, prune, import, expiry,
   compaction, reset, rollback, and white-box tests.
3. Run focused race validation with an anchored alternation containing at
   least:
   - `TestCacheCanonicalEquivalentSwapRacesSessionRelease`
   - `TestCacheConcurrent`
   - any deterministic concurrent release/prune test directly modified or
     added by the implementation
4. Do not run `go test ./...`.
5. No persistence generation is expected. A schema or generated persistence
   diff is a scope violation and must stop for review.
6. Run `git diff --check` and inspect `git status` to verify only the intended
   DagQL implementation/tests and internal documentation changed.

Use the consistency helper at stable checkpoints in the focused tests. Do not
add a broad randomized stress harness unless a deterministic coverage gap is
found during implementation.

## Bounded Linux Performance Validation

Use the same Linux-VM class and reviewed harness as the existing evidence.
Each point runs in a fresh `go test` process, not through the full screen.
Mirror the runner settings:

- `DAGGER_EGRAPH_BENCH_SCALE=<scale>`
- `go test ./dagql -run '^$'`
- an anchored `-bench` expression
- `-benchtime=1x -count=1 -v`
- `/usr/bin/time -v`
- `timeout --signal=TERM 75s`
- full serial observation as provided by the benchmark
- a new output directory with raw stdout/stderr, exit status, commit and
  environment manifest, and SHA-256s

Run exactly these six processes initially:

1. `BenchmarkCacheEGraphRelease/wide-output/transient/256`
2. `BenchmarkCacheEGraphRelease/wide-output/transient/512`
3. `BenchmarkCacheEGraphPrune/wide-output/persisted-fresh/256`
4. `BenchmarkCacheEGraphPrune/wide-output/persisted-fresh/512`
5. `BenchmarkCacheEGraphRelease/wide-output/imported/256`
6. `BenchmarkCacheEGraphRelease/independent/transient/50000`

The six 75-second process caps give a 7.5-minute mechanical ceiling. Existing
R256/R512 and 50,000-result points ordinarily finish well inside that bound.
Do not collect a profile unless a result is anomalous and bounded review agrees
that attribution is necessary.

### Acceptance interpretation

- All focused functional and race tests pass.
- The consistency helper reports no drift or orphaned derived entries.
- Fixture shape, result counts, posting counts, lock attempts, and dropped
  observation counts remain consistent. Any changed benchmark counter must be
  explained by the new removal algorithm rather than fixture drift.
- Transient wide release must be clearly below its existing ranges:
  - R256 baseline: 14.748-16.817 ms.
  - R512 baseline: 64.239-69.060 ms.
- Persisted-fresh prune must be clearly below its existing pre/post values:
  - R256: 14.838-15.557 ms.
  - R512: 64.927-65.870 ms.
- The R256-to-R512 transient and persisted-fresh curves must no longer show
  the old roughly-fourfold removal slope.
- Imported release R256 remains a broad-cleanup arm. Its baseline is
  45.339-47.030 ms. It may improve because the second class-wide survivor
  pass is gone, but this slice must not claim to remove its real posting work.
  It must not materially regress.
- Independent transient 50,000 is the ordinary control. Its two existing
  observations are 113.520 and 121.157 ms; it must not materially regress.
- Do not invent a percentage threshold. Judge exact structural counters and
  clear separation from the existing ranges. If process noise makes one
  elapsed-time comparison ambiguous while its counters are correct, repeat
  only that exact arm once.
- Any correctness, race, schema, or unexplained fixture/counter failure blocks
  merge. Ambiguous timing alone calls for the one bounded repeat, not a larger
  matrix or redesign.

## Compatibility and Migration

- There is no public API or wire-format change.
- There is no persistence schema/version change and no migration.
- Existing stores import as before. Imported result/output-class associations
  are reconstructed into both directions, imported class-wide digest postings
  are marked broad, and removal keeps the safe class-scan fallback.
- Persistence snapshots continue to use normalized output classes. Eager
  in-memory forward canonicalization therefore changes no persisted meaning.
- Exact and broad indexes are derived in-memory state and are reset with the
  cache.
- Lookup routes, hit provenance, candidate sets, result IDs, ownership counts,
  and expiry behavior remain compatible.
- The coarse metadata estimate formula remains unchanged for this slice.

## Documentation

Update `internal-docs/egraph.md` to document:

- forward and inverse result/output-class associations;
- canonical-root maintenance during merge and compaction;
- exact versus broad digest-posting bookkeeping;
- exact removal, imported broad fallback, and the unexpired survivor check;
- reset responsibilities.

Update `internal-docs/cache_persistence.md` to document:

- the current schema does not encode exact per-result digest membership;
- import reconstructs class-wide postings and marks imported results broad;
- there is no schema/version/migration change in this slice;
- persisting exact associations is a possible separately authorized later
  optimization.

Update `internal-docs/cache_pruning.md` only if it explicitly describes the old
class-scan implementation. Do not add speculative material. Leave the
benchmark harness and `internal-docs/egraph-contention-benchmarks.md` unchanged
unless an implementation review identifies a factual documentation error.

## Diagnostics and Rollback

No feature flag is required. The change is internal derived bookkeeping, and
the two commits provide the rollback boundary.

- If exact reverse bookkeeping is wrong, revert commit 2. Broad class-scan
  behavior remains correct.
- If inverse association or survivor behavior is wrong, revert commit 1, or
  follow the documented mechanical revert order if the final commits depend
  on each other for compilation.
- Preserve all pre-change and post-change raw benchmark artifacts and
  manifests for comparison.
- Use concrete IDs from the test-only consistency checker to identify the
  posting or association mutation site that drifted.
- Do not hide a missing exact runtime entry by marking all runtime results
  broad. That would mask the bug and restore the contention.
- Existing benchmark observer counters and the test-only checker are
  sufficient diagnostics. Do not add production metrics or debug APIs in
  this slice.

## Later Directions and Authorization Boundary

### Exact persistence associations

The smallest evidence-supported later direction is to persist exact
per-result digest associations, or enough provenance to reconstruct them.
That would let import avoid all-to-all output-class postings and would target
both imported removal and imported lookup/ID pathologies.

It is deliberately not part of this slice because it changes the persistent
representation and requires:

- schema/version design;
- backward-compatible import or migration behavior;
- persistence snapshot/write changes;
- restart compatibility tests;
- separate bounded performance validation.

Erik must authorize that scope separately.

### Other deferred directions

- Optimize popular-input congruence repair only if it remains important after
  removal improves and focused evidence justifies it.
- Reconsider lock partitioning only if narrow algorithmic fixes leave
  meaningful contention. Lock partitioning carries substantially greater
  ownership and equivalence risk.
- Continue rejecting tombstones, unbounded lazy posting cleanup, and
  asynchronous release unless a separately reviewed semantic design calls for
  them.

Stop and escalate before implementation if the narrow slice unexpectedly
requires a core ownership/equivalence semantic change, persistence schema
change, release/prune visibility change, or material scope expansion.

## Review Ledger

The implementation plan was authored by the coordinating Codex agent and
reviewed by a fresh independent Fable 5 agent at x-high effort in its own
private Dagger worktree. The old Fable researcher was neither contacted nor
reused.

### Independent investigation

The reviewer independently checked repository instructions, cache and e-graph
documentation, the ownership/release/lookup/prune/import implementations, the
reviewed harness commits, historical correction transcripts, manifests,
generated summaries, representative raw files, and profiles. It concluded
that the evidence is useful and trustworthy for the practical engineering
question and that no benchmark rerun was needed for planning.

It confirmed the central mechanism while distinguishing two cases:

- transient removal mostly performs wasted class traversal around a small
  exact posting set;
- imported removal has millions of real broad memberships and cannot be fully
  solved by an exact runtime reverse index.

It agreed that wide-class removal is the correct first direction and rejected
lock sharding, tombstones, and a wholesale class-keyed lookup redesign for the
first slice.

### Adjudicated disagreements

The reviewer initially proposed placing reverse state on `sharedResult` and
considered allowing expired-only classes to retain terms until later
collection. The coordinator rejected both:

- derived state belongs to `Cache` because cache reset and result-wrapper
  lifetimes differ;
- the inverse survivor check must evaluate expiry and preserve current cleanup
  timing.

The reviewer rechecked the source and accepted both corrections.

It then identified implementation traps incorporated here:

- maintain inverse state through every merge, compaction, import, removal,
  and reset;
- route all production posting writes through one helper;
- preserve the pre-existing early-return anomaly rather than silently changing
  semantics;
- narrow the unsupported direct-map white-box contract explicitly;
- check orphan liveness in rollback and collection paths;
- keep persistence exactness as a separate authorization boundary.

### Final adversarial review and artifact corrections

The reviewer approved the design but rejected several evidence citations in a
draft plan. It found:

- a purported transient-R512 forced-contention run and large mutex-profile
  attribution that do not exist in the preserved artifacts;
- R256 imported timings combined incorrectly with R2048 membership counts;
- unrecorded derived digest-loop counts presented as measured counters;
- several mistranscribed millisecond values;
- a nonexistent benchmark arm named `wide-output/imported-object/256` rather
  than the real `wide-output/imported/256`;
- a shortened nonexistent test name rather than
  `TestCachePruneDoesNotProtectTermProvenanceOnlyResultFromActiveResult`.

The coordinator independently checked these findings against
`practical-comparison.tsv`, contention and confirmation summaries, profile
summaries, raw benchmark output, and the local source. This document contains
the corrected values and identifiers. No correction changed the optimization
design.

The reviewer also resolved two final design details:

- keep the coarse metadata formula unchanged and avoid duplicating imported
  reverse strings;
- eagerly rewrite only the losing inverse set's forward associations during
  equivalence merge, which is compatible with normalized persistence
  snapshots.

The reviewer genuinely approved the resulting design, scope, data structures,
helper routing, algorithms, invariants, sequencing, tests, validation,
documentation, rollback, and deferral boundaries. There is no unresolved
material disagreement or escalation at the start of implementation.
