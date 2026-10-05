# dagql cache TLA+ model

A TLC-checked model of the dagql cache's concurrency kernel: lookup,
in-flight call deduplication, publication, session ownership and release,
per-session release completion waits,
the read barrier, lazy evaluation, persistence (import, decode, flush,
restart), and calls issued by detached call executors.

The spec is `CacheLifecycle.tla`. It is self-contained: its header
explains the modeling rules, and every action's comment names the Go code
it models. Each `CacheLifecycle_*.cfg` checks one scenario; the comment
at the top of each config says what the scenario is and whether the run
is expected to pass or to violate one named invariant. Expected
violations are reserved for deliberate mutations that prove a gate can
fail, and for deliberately accepted model findings.
`CacheLifecycle_orphaned_lease.cfg` restores the release rule under
which a completed call's operation and client leases were orphaned when
its last waiter left through cancellation, and must violate
`SharedLeaseReleasedWhenRetired`. None
is tracked today: every configuration is a green regression check, and
the `expectedOutcome` map is the authoritative list. (The most
recently closed findings: `attach_release_reader` — a session's release
manufacturing failures for live, innocent callers through the
attachment machinery — fixed by classifying the producer-release
barrier error so parked readers convert their hit to a miss and execute
the call themselves (the same retry shape as the fixed `decode_cancel`
joiner finding), and by claiming the attachment target under the graph
lock before any unlocked refresh work, with target selection pinned by
the claim-at-acquisition invariant so no other session's release can
collect a target out from under its claim;
`resources_requirement_growth` — a
result's stored requirement set growing after the lookup filter ran —
fixed by a serve-time re-validation keyed on a per-result requirement
generation captured at selection (explicit retention edges accept
requirement-carrying deps again and cascade the growth to ancestors;
`resources_latedep_recheck` covers that serve window and
`resources_latedep_cascade` the ancestor cascade, each from an
imported starting graph);
`resources_restart` — the stored requirement set drifting from the true
transitive requirement — fixed by recomputing dependency-first at
import and leaving the stored set alone at decode install; and
`decode_cancel` — a decode leader's own cancellation failing its parked
joiners — fixed by retrying a departed leader's cancellation and the
post-install lease sync.)

Run the checks (the module is dev-env scoped and deliberately does NOT
run in CI):

```sh
# fast subset (~1 minute): the right default while iterating
dagger --env dev check tla-check:quick

# chosen configurations, expectations enforced
dagger --env dev call tla-check some --configs=resources,resources_latedep

# one configuration, raw TLC output, optional probe injection
dagger --env dev call tla-check one --config=resources

# the full suite: REQUIRED before pushing changes under dagql/tla,
# expensive otherwise - well over an hour wall with four TLC JVMs; the
# largest configurations each exceed 40 million distinct states
# (resources_requirement_growth ~114M, resources_restart ~110M,
# lazy_import ~62M, resources_latedep_cascade ~57M, persist ~47M)
dagger --env dev check tla-check:cache-lifecycle
```

Configuration budget: target every configuration under ~20 minutes on a
dev box. The scenario-scoping constants (`ReleaseSessions`,
`PersistableIntent`) narrow which external events a configuration
explores, in the existing style — configurations select scenarios, never
implementations — and their defaults preserve every prior state space
exactly. When a configuration is re-scoped, its comment records the
one-time exhaustive bound the scope replaced (`attach_release_reader`:
~863M distinct states unscoped, ~26M scoped; its re-break evidence was
re-verified at the reduced scope). A configuration whose `ReleaseSessions`
is a proper subset of `Sessions` must not declare session symmetry
(`SymmCalls` instead of `Symm`).

Every run compares each configuration's outcome to the `expectedOutcome`
map in `.dagger/modules/tla-check/main.go` (a new config must be added
to that map; cheap ones belong in `quickConfigs` too). On failure, the
message names the configuration and how its outcome diverged from the
expectation. Because CI no longer runs any of this, the full suite
before pushing is the only line of defense: do not skip it.

Container part persistence is opt-in through `ModelContainerPartPersistence`.
Existing configurations set it to false. Their new fields have one fixed inert
value, and routing reduces to the original part mapping. The enabled model
compares the encoder's consumed-group projection with separate body-success
evidence, retains saved descriptors across two process epochs, and routes saved
snapshots to independent local opening groups. A second flush can preserve an
unopened typed value or an encoded envelope.

The direct metadata owner represents the caller's operation lifetime while it
scans parent copies. It has no part, cache completion, or bookkeeping callback.
Demand evidence belongs to the requested container and part. The explicit parent
request made by a copy records demand on the parent; selecting the child copy
records no demand on that child. This distinction lets an incorrect stored-copy
selection fail the opening assertion itself.

For this feature, the approved selected validation plan supersedes the full-suite
instruction above. The completed set is all 18 `quickConfigs`, the exact `lazy`
anchor, `lazy_parts`, `lazy_parts_prereq`, `lazy_parts_delegate`, and the three
container configurations. No full suite was run. None of the new configurations
is added to the quick set.

The following evidence was recorded on 2026-09-05 UTC against model SHA-256
`c397ada06da89afd1ea2f8e171bc93f50bb86ea8e26656b0ce2a808687a8c6b0`.
The saved `run_tlc.py` used the module's pinned TLC 1.7.4 jar and invocation:

```sh
java -Xmx8g -XX:+UseParallelGC -cp /tmp/tlatools/tla2tools.jar tlc2.TLC \
  -workers auto -deadlock -config model.cfg CacheLifecycle.tla
```

Each run has its own source/config, `command.json`, `result.json`, and
`output.log` under
`/tmp/per-part-persistence-implementation-20260905/implementer`.
`final-bounded-summary.log` contains 24 passing runs; the separate
`final-sweep-container_sweep_restart-20260905T043003` supplies the 25th.
All finished with an empty queue and the registered successful outcome.

| Configuration | Distinct states | Wall seconds |
| --- | ---: | ---: |
| `lazy` | 6,398,997 | 80.23 |
| `container_part_restart` | 13,501,639 | 146.81 |
| `container_joint_restore` | 3,491,003 | 42.09 |
| `drain_nested_call` | 14,833 | 4.34 |
| `drain_orphan` | 14,833 | 2.58 |
| `flush_closure` | 119,670 | 3.50 |
| `flush_drained` | 38,684 | 2.79 |
| `flush_inflight` | 141,104 | 3.49 |
| `flush_roundtrip` | 53,818 | 2.48 |
| `lazy_liveness` | 6,977 | 3.69 |
| `lazy_parts_liveness` | 145,457 | 43.53 |
| `lazy_parts_release` | 238,825 | 33.53 |
| `lazy_release` | 1,505 | 1.82 |
| `lazy_stale_cancel` | 28,017 | 2.68 |
| `liveness` | 5,365 | 2.99 |
| `lost_cancel` | 45 | 1.17 |
| `orphan_edges` | 267 | 1.17 |
| `attach_error` | 6,999 | 1.48 |
| `attach_error_restart` | 22,650 | 1.83 |
| `release_inflight` | 35,565 | 7.35 |
| `release_claim_race` | 35,565 | 2.48 |
| `lazy_parts` | 3,821,617 | 35.77 |
| `lazy_parts_prereq` | 8,251,257 | 75.11 |
| `lazy_parts_delegate` | 22,518,204 | 275.96 |
| `container_sweep_restart` | 394,070 | 14.63 |

The disabled-feature anchor remains exactly 6,398,997 states. The affected
existing shapes also retain their recorded state counts.

Successful BFS reachability probes in `final-probes-summary.log` deliberately
assert the negation of each desired witness. Each stopped at the named probe
violation (exit 12); these are reached witnesses, not exhaustive passing checks.

| Witness | Probe violated | Distinct states | Wall seconds |
| --- | --- | ---: | ---: |
| `fresh_sweep_roundtrip` | `NoFreshSweepRoundTrip` | 139,034 | 5.10 |
| `direct_sweep` | `NoDirectSweep` | 1,420 | 2.24 |
| `stored_sibling_sweep` | `NoStoredSiblingSweep` | 811 | 1.83 |
| `stored_open` | `NoStoredOpen` | 1,441 | 1.53 |
| `stored_open_after_restart` | `NoStoredOpenAfterRestart` | 199,203 | 4.05 |
| `open_join` | `NoOpenJoin` | 10,205 | 3.14 |
| `open_retry` | `NoOpenRetry` | 45,946 | 2.90 |
| `open_release` | `NoOpenRelease` | 30,119 | 2.78 |
| `typed_second_flush` | `NoTypedSecondFlush` | 174,346 | 4.27 |
| `envelope_second_flush` | `NoEnvelopeSecondFlush` | 135,233 | 3.92 |
| `joint_independent` | `NoIndependentJointOpen` | 1,306,158 | 13.61 |

The fresh sweep witness reaches depth 56: an eager parent, fresh lazy child,
actual fs copy, capture with `parts = observed = {pMeta, pFS}` while the fs copy
has no cache completion, actual Restart, then a successful saved fs opening.
It does not prove the real mounted mutation with a pending ancestor exec; Go
restart evidence must establish that case.

Final deliberate breaks in `final-breaks-summary.log` all stop at their intended
assertion (exit 12). The two Restart mutations fail at the actual import
boundary. The separate invented-completion mutation fails at an imported start.

| Mutation | Assertion violated | Distinct states | Wall seconds |
| --- | --- | ---: | ---: |
| `restart_drop` | `ContainerDecodePreserves` | 3,541 | 2.13 |
| `restart_invent` | `ContainerDecodePreserves` | 4,265 | 2.23 |
| `drop_completion` | `ContainerCaptureExact` | 1,872 | 1.73 |
| `cache_only_capture` | `ContainerSweepCaptureExact` | 19,751 | 2.28 |
| `invent_completion` | `ContainerDecodePreserves` | initial state | 1.53 |
| `recompute_stored` | `StoredPartsNeverComputed` | 1,497 | 1.73 |
| `open_both` | `StoredOpenRequiresDemand` | 842,899 | 14.34 |
| `stored_copy` | `StoredOpenRequiresDemand` | 1,139 | 2.60 |
| `open_accessor_capture` | `ContainerCaptureExact` | 3,819 | 2.60 |
| `drop_partial_root` | `ContainerClosureComplete` | 2,133 | 2.19 |
| `retry_body` | `StoredOpenNotRepeated` | 33,059 | 3.54 |

`recompute_stored` removes routing and original-group seeding together;
`open_both` opens both members of a joint output; `stored_copy` treats an opening
key as a parent copy; `retry_body` repeats a successful opening after failed
bookkeeping. Each mutation and its trace are retained with its run.

Scope limits are explicit. `ContainerSweepScenario` fixes parent/child call
roles, eager fresh parents, child dependencies, one external evaluator per
process, and room for an internal copy. It has no call symmetry and reads no
saved/captured completion evidence. Imported starts include a pending child
copy beside a closed stored sibling. Release starts after both calls exist;
publication/release races remain in the existing release shapes. Non-final
parent skipping remains covered by `lazy_parts_delegate` and `SweepStartsFinal`.
The joint shape has one evaluator; joining, failure, retry, and release use the
part shape. A direct metadata visit is bounded to once per result/process;
Go permits repeated visits. One evaluator follows one constituent of a whole
request; Go tests must check whole-result return readiness and two actual
restarts, including typed and untouched-envelope middle processes.

Earlier constrained simulations in `final-sweep-probes-summary.log` and
`final-sweep-breaks-summary.log` did not reach their predicates. They are not
positive evidence. Earlier broad-cost joint and sweep runs were stopped with
queued states; they are incomplete cost measurements, not successful bounds.

The independent `SnapshotChain.tla` component covers immutable chain import and
export below DagQL. `snapshot_import` and `snapshot_export` are registered short
names for `Some` and `One`. Existing short names still select `CacheLifecycle`.
The cache spec and its existing configurations are unchanged.

```sh
dagger --env dev call tla-check some --configs=snapshot_import,snapshot_export
dagger --env dev call tla-check one --config=snapshot_import
```

The component separates snapshot/index presence, handles, actual resources,
source reads, and layer apply. It models partial ancestry attachment and a
presence check after attachment. containerd's resource insertion validates no
target presence; its metadata writes serialize with GC. A lost candidate may
leave temporary references to absent resources, but cannot be returned as a hit.
Selection evidence is recorded at the reuse decision, since another export can
publish after an importer accepts a miss. Existing and generated export blobs
remain pinned until provider consumption. Imported refs remain pinned through
owner adoption. Snapshot IDs change on reapply, preserving actual parent keys.

The two bounds allow two importers, two owners, prefix/suffix chains, failure,
pruning, and later apply. The import shape allows three requests and three
physical creations. The export shape starts with two local layers and allows
two imports and one further creation. File bytes, metadata, compression,
containerd resources, actual cleanup, and typed adoption/restart require Go
checks. These bounds do not establish those facts.

For the bounded snapshot implementation, run the existing quick set through the
changed runner and the relevant snapshot shapes, probes, and deliberate breaks.
The approved selected plan supersedes the full-suite instruction for this work.
Every individual run has a 30-minute ceiling; measure before increasing bounds.
Measured on 2026-09-05 with the runner's pinned TLC jar, Java 21, 8 GiB heap,
and 16 workers: import reached 475,119 distinct states (1,295,189 generated)
in 4.51 seconds; export reached 5,185,181 (17,628,508 generated) in 24.80 seconds.
The existing quick set passed all 18 shapes through the changed runner in
77.19 seconds including Dagger startup, using `dagger --env dev check tla-check:quick`.
The two snapshot short names also passed through `Some` in 74.74 seconds.
These runner checks preceded the final owner-content refinement; the counts
above are the direct TLC runs of the final behavioral source. All 42 existing
cache source/config files remain byte-identical to the private baseline.

Private controls retain the selected snapshot handle but send it to byte work,
omit terminal producer cleanup, remove exclusion, skip snapshot/blob selection,
skip presence validation, omit blob/ancestor pins, omit export registration,
omit the existing producer blob pin, and retain failed operation resources.
All eleven violate their intended assertions. The first source checkpoint
missed the first two controls; the revised evidence and cleanup assertions
reject both. Nine basic reachability probes also reached their named states.
Three earlier probes record ordered events for the same snapshot: reuse
after prefix failure, consumer adoption after another owner releases, and
provider consumption after another owner releases. These reached 3,653,
15,236, and 246,413 distinct states in 1.22, 1.52, and 2.79 seconds, before
the continuing-owner refinement below. Their source copies remain in the evidence.

Commands, source/config hashes, logs, mutation copies, and traces are under
`/tmp/snapshot-foundations-implementation-20260905/implementer`. Early malformed
scratch probes are recorded as errors, not evidence. The model has a normalized
root already; root omission and requested compression require Go controls.
The local-build shape starts with one ordinary owner of the original
snapshot ancestry. Its first export must attach new content to that continuing
owner. Evidence records present content at adoption and extends that expectation
on export registration. It does not demand missing historical bytes merely to
reuse a usable snapshot.
The continuing-owner witness reaches provider consumption and producer release
with both new blobs still owned (40,429 states, 1.72 seconds). Omitting the
owner content update violates `OwnerHasRecordedContent` (8,130 states, 1.53
seconds). All prior eleven controls and nine basic probes were repeated on
this source and reached their intended assertions.

The single producer does not establish concurrent export cancellation. Focused
Go race checks use real containerd metadata, native snapshots, content stores
and leases for active/waiting export cancellation and waiting import cancellation.
They consume the surviving provider after canceled-owner cleanup. Separate Go
checks exercise mounted apply failure, source failure, actual GC, owner release,
and usable snapshots whose historical blob disappears during attachment.
The typed check clears manager rows before SQLite hydration and reimports the
chain with the original snapshot ID and unchanged read/apply counts. This is a
local cache/manager reopen witness, not a full engine restart run.

The startup ownership boundary is also checked in Go. A retained imported ref
leaves its private lease at clean cache close. After SQLite restores durable
owners, startup removes only leases marked as snapshot transfers. An unrelated
temporary lease survives; GC and reimport retain the original snapshot with no
additional reads or applies. The test leaves the old ref unreleased, matching
the process lifetime endpoint. The snapshot model does not include this startup
sequence; `core-restart-race-20260905T085338` records the physical witness.

`CacheLifecycle` explicitly omits arbitrary-cache initialization. Its existing
operation accounting contract is checked in Go: a canceled arbitrary initializer
that returns late retains a cache operation through callback cleanup, so Close
waits for the initializer and its release. Deterministic tests include blocked
release, cleanup errors, other live waiters and replacement entries. A real
prepared image lease check covers the provider consumer. These checks do not
expand the old model's state spaces or prove its omitted implementation paths.

Private production controls remove export reuse registration and, separately,
restore an omitted root as the reuse parent. The same-manager observations
reject both with one unexpected apply; scratch and known-empty roots both reject
the second control. Omitting startup transfer deletion also fails the expected
surviving-lease count in `restart-cleanup-control-20260905T085558`.
Controls remain outside production source. The evidence root
contains `production-controls.json`, per-run command manifests, source copies and
logs. Focused race evidence is in `snapshot-race-20260905T084725`,
`core-race-20260905T084859` and `dagql-race-20260905T083624`. Provider race checks
are in `prepared-race-20260905T085744` and `assembled-race-20260905T085911`.
`final-evidence.md` reconciles source revisions, commands, counts and limitations.
No full TLA or Go suite was run for this implementation.

## One current entry per recipe

The model keeps at most one current entry per call, the entry the recipe
index names (`Current`; the call identity stands for the recipe digest).
The rules, and where the engine applies them (`initCompletedResult`,
`dagql/cache_current_entry.go`):

- **Adoption.** `PubIndexFresh` registers and indexes a fresh value only when
  its call has no current entry. `PubAdoptSameCall` adopts the call's live
  entry, one whose attachment has settled clean, and takes the handoff hold
  in the same step, as `PubAdopt` does. While that attachment is open, no
  publication action is enabled, so publication waits, unless the publishing
  session has been released: then `PubIndexFresh` registers the value beside
  the entry, not indexed (`ReleasedWhileCurrentOpen`), so the release doesn't
  wait on another session's attachment. Adoption keeps the
  existing session filter. When the publishing session does not cover the
  entry's stored requirements, `PubIndexFresh` registers the value beside the
  entry, live but not indexed. A late retention edge can raise those
  requirements without changing the call.
- **Expiry.** `Expire` (`AllowExpire`) marks a value expired: time with no
  clock.
  - `PubReplaceInPlace` installs the new value into an expired current entry
    that nothing uses, under the same number, and counts the replacement.
  - `PubRetire` takes an expired entry that something uses out of the index;
    the next `PubIndexFresh` registers the new value.
  - The guard, `UsedByOwnership`, is the engine's own check: ownership beyond
    the retention edge, or a session record. The action property
    `NoReplaceUnderUser` judges every replacement against the full
    definition, `InUse`: sessions, dependent entries, handoff holds, lazy
    attempts, decodes and evaluators. The recipe configurations run without
    lazy evaluation (`ModelLazy` off), so its lazy-attempt clause is not
    exercised there; `TestCachePublicationRetiresAnExpiredEntryUnderLazyEvaluation`
    in `dagql/cache_current_entry_test.go` covers a running lazy attempt as a
    use.
  - A replacement whose new attachment fails also drops the entry's retention
    edge, in `PubAttachFailDropHold`.
- **Live merge.** `MergeLive` (`ModelMerge`) merges one unexpired root record,
  optionally with one dependency record of another call. Each record targets
  its call's current entry by the same rules: create, keep, replace or retire.
  Merge waits while a target's attachment is open, and every root gets a
  retention edge.
- **Restart.** `Flush` and `Restart` keep each entry's expiry, whether the
  index named it (`indexed`), and its replacement count. Boot indexes exactly
  the entries that were indexed.

Invariants:

- `OneLiveEntryPerCall`: no two indexed, unfailed entries of one call.
- `NoPersistedAttachErroredResult` also states the design's
  `NoRetainedFailedEntry`.
- `NoReplaceUnderUser`, an action property. The tla-check runner recognises
  action-property violations.

Every existing configuration sets `AllowExpire` and `ModelMerge` off. D1
itself is always on, so configurations where one call can publish twice now
adopt instead.

Imported rows (`ImportIndexed`): a saved store holds at most one indexed row
per call, and any number of unindexed ones (dependencies, rows the session
filter kept out of the index, retired rows).

- Under `AllowExpire` the row sets choose the flag.
- Without it, each call's first row is indexed and its later rows are not.
  That is exactly the base model's import space, with no index dimension
  added.
- This leaves out stores whose current entry is a later row of its call, or
  that have none. The recipe configurations' focused rows
  (`RequirementImportRows`, `DependentImportRows`) and `recipe_expiry_deps`,
  which chooses the flag freely, cover those.

**D1 off reproduces the base exactly.** A private variant in which the recipe
index never names an entry (every write of `indexed` is FALSE) leaves
adoption, the fresh-publication guard, retirement and replacement nothing to
act on. Its state space must equal the model's before this change: that
shows the lower counts with D1 come from D1, and that no other action is lost.
Each run went to completion:

| Configuration | Before this change | D1 off | With D1 |
| --- | ---: | ---: | ---: |
| `liveness` | 5,365 | 5,365 | 4,941 |
| `lazy` | 6,398,997 | 6,398,997 | 2,601,301 |
| `persist` | 46,871,600 | 46,871,600 | 9,337,888 |

This check found an earlier version of the import rows that allowed at most
one row per call when `AllowExpire` was off: D1 off then gave `persist`
550,006 states.

| Configuration | Question | Distinct states | Wall seconds |
| --- | --- | ---: | ---: |
| `recipe_adopt` | two sessions on one call, with an attachment failure | 2,579,792 | 45.1 |
| `recipe_adopt_liveness` | the waiting publication still terminates | 30,205 | 11.8 |
| `recipe_adopt_filter` | the session filter, across a restart (`RequirementImportRows`) | 159,209 | 9.4 |
| `recipe_expiry` | replacement, retirement and a failed replacement, with release | 8,131,336 | 188.0 |
| `recipe_expiry_deps` | a restored dependent, encoded or decoded (`DependentImportRows`) | 792,543 | 22.4 |
| `recipe_expiry_restart` | a restart after a retirement whose current entry also expired | 119,236 | 5.2 |
| `recipe_merge` | `MergeLive` racing publication, release, expiry and a failed attachment | 2,276,670 | 157.7 |

`recipe_expiry_restart` is bounded to one publication before its restart,
with `DrainOnRelease`. The variant with a call after the restart was stopped
at its 1,000-second bound, at about 37 million states with its queue still
growing. Publication after a restart is covered by the Go restart tests in
`dagql/cache_current_entry_test.go`.

Re-breaks mutate one rule in a private copy of the model, and each must trip
its invariant:

| Mutation | Configuration | Violated | Distinct states |
| --- | --- | --- | ---: |
| `PubIndexFresh` beside a live entry of its call | `recipe_adopt` | `OneLiveEntryPerCall` | 21,507 |
| the unindexed registration indexed | `recipe_adopt_filter` | `OneLiveEntryPerCall` | 267 |
| `PubReplaceInPlace` under any user | `recipe_expiry` | `NoReplaceUnderUser` | 60,253 |
| `PubReplaceInPlace` with a dependent present | `recipe_expiry_deps` | `NoReplaceUnderUser` | 37,298 |
| a failed replacement keeps its retention edge | `recipe_expiry` | `NoPersistedAttachErroredResult` | 827,309 |
| `MergeLive` onto an open attachment | `recipe_merge` | `NoPersistedAttachErroredResult` | 5,485 |

Reachability probes assert the negation of a witness, and each stopped at it:

- by publication: an adoption; an unindexed registration, also after a
  restart; a replacement; a retirement for a dependent; a failed replacement
  collected;
- by merge: a kept published entry, a replacement, and a retirement.

The merge probes disable the publication replace and retire actions, so a
witness can only come from merge.

**The full suite on this model.** Every configuration in the tla-check
module's `expectedOutcome` map ran one at a time on the committed
specification, with the pinned TLC 1.7.4 and 6 workers. Wall seconds
exclude waits between runs. Every run ended with its expected outcome,
except `resources_restart`, which stopped at its cap:

| Configuration | Expected outcome | Distinct states | Wall seconds |
| --- | --- | ---: | ---: |
| `snapshot_import` | clean | 475,119 | 7.0 |
| `snapshot_export` | clean | 5,185,181 | 39.5 |
| `release_prune` | clean | 48,956,036 | 938.4 |
| `liveness` | clean | 4,941 | 3.4 |
| `lazy` | clean | 2,601,301 | 76.7 |
| `lazy_liveness` | clean | 6,977 | 3.0 |
| `lazy_stale_cancel` | clean | 28,017 | 2.4 |
| `lazy_import` | clean | 168,076,353 | 4,191.3 |
| `persist` | clean | 9,337,888 | 159.2 |
| `persist_liveness` | clean | 2,313,667 | 516.8 |
| `flush_roundtrip` | clean | 55,370 | 2.4 |
| `orphan_edges` | clean | 538 | 1.3 |
| `release_claim_race` | clean | 85,670 | 3.3 |
| `drain_orphan` | clean | 25,503 | 2.2 |
| `rollback` | clean | 43,532,245 | 744.2 |
| `rollback_decode` | clean | 100,445,634 | 1,936.3 |
| `lost_cancel` | clean | 45 | 1.4 |
| `attach_error` | clean | 6,095 | 1.8 |
| `attach_error_adoption` | clean | 17,010,279 | 333.5 |
| `attach_error_restart` | clean | 27,978 | 2.2 |
| `flush_closure` | clean | 148,890 | 4.4 |
| `release_inflight` | clean | 85,670 | 10.9 |
| `drain_nested_call` | clean | 25,503 | 4.2 |
| `flush_inflight` | clean | 269,558 | 6.6 |
| `flush_drained` | clean | 74,948 | 3.1 |
| `lazy_release` | clean | 2,806 | 1.9 |
| `release_wait` | clean | 538 | 1.6 |
| `orphaned_lease` | violates `SharedLeaseReleasedWhenRetired` | 192 | 1.1 |
| `lazy_parts` | clean | 3,821,617 | 80.0 |
| `lazy_parts_prereq` | clean | 8,251,257 | 258.9 |
| `lazy_parts_liveness` | clean | 145,457 | 42.5 |
| `lazy_parts_delegate` | clean | 18,537,076 | 571.7 |
| `lazy_parts_release` | clean | 424,114 | 43.6 |
| `container_part_restart` | clean | 34,893,206 | 1,255.2 |
| `container_sweep_restart` | clean | 678,052 | 52.4 |
| `container_joint_restore` | clean | 5,660,086 | 167.4 |
| `decode_cancel` | clean | 2,087,399 | 39.5 |
| `decode_cancel_liveness` | clean | 2,087,399 | 480.5 |
| `resources` | clean | 27,608,598 | 520.6 |
| `resources_latedep` | clean | 3,510,650 | 107.6 |
| `resources_requirement_growth` | clean | 4,642,858 | 108.2 |
| `resources_latedep_recheck` | clean | 44,941,765 | 1,190.4 |
| `resources_latedep_cascade` | clean | 13,592,856 | 250.1 |
| `attach_release_reader` | clean | 25,893,992 | 372.3 |
| `recipe_adopt` | clean | 2,579,792 | 70.2 |
| `recipe_adopt_filter` | clean | 159,209 | 6.7 |
| `recipe_adopt_liveness` | clean | 30,205 | 7.5 |
| `recipe_expiry` | clean | 8,131,336 | 164.4 |
| `recipe_expiry_deps` | clean | 792,543 | 18.1 |
| `recipe_expiry_restart` | clean | 119,236 | 5.1 |
| `recipe_merge` | clean | 2,276,670 | 140.8 |
| `resources_restart` | clean | stopped at 274,152,504 | 5,401.1 (cap) |

- `resources_restart` stopped at its 5,400-second cap with no violation
  found: 274,152,504 distinct states at depth 34, with 19,329,143 still
  queued and the queue shrinking slowly. The count is above the ~110M
  recorded for it before this change. The likely reason, not measured:
  with session handles set, as in the `resources` configurations, the
  session filter's unindexed registrations and the index state add states
  that D1 does not otherwise remove.
- `lazy_import` ran under a 10,800-second cap and finished in 4,191
  seconds. An earlier run of it was lost to a tooling error at about 89
  million states and is not counted.
- `release_prune` took 938 seconds, within the 20-minute budget above; an
  earlier run on a busier machine took 29 minutes.
  Several import and restart configurations exceed that budget:
  `lazy_import`, `rollback_decode`, `container_part_restart` and
  `resources_restart`.

Scope limits:

- Merge bundles carry one dependency at most.
- Incoming records are never expired: merge skips an expired root, and the
  both-expired row of the merge table is not modeled.
- Merge configurations bind no session-resource handle.
- Other caches' holdings are not modelled. `UsedByOwnership` matches the
  engine's `resultInUseLocked`, which leaves holdings out, only for entries
  without holdings; the engine's Go tests cover entries with them.

The TLC runs' outputs are not committed.
