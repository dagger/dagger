# dagql cache TLA+ model

A TLC-checked model of the dagql cache's concurrency kernel: lookup,
in-flight call deduplication, publication, session ownership and release,
the read barrier, lazy evaluation, persistence (import, decode, flush,
restart), and calls issued by detached call executors.

The spec is `CacheLifecycle.tla`. It is self-contained: its header
explains the modeling rules, and every action's comment names the Go code
it models. Each `CacheLifecycle_*.cfg` checks one scenario; the comment
at the top of each config says what the scenario is and whether the run
is expected to pass or to violate one named invariant. Expected
violations are reserved for deliberately accepted model findings. None
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
and 16 workers: import reached 475,119 distinct states in 4.25 seconds; export
reached 3,358,215 in 22.85 seconds. The existing quick set passed all 18 shapes
through the changed runner in 77.19 seconds including Dagger startup.

Private controls retain the selected snapshot handle but send it to byte work,
omit terminal producer cleanup, remove exclusion, skip snapshot/blob selection,
skip presence validation, omit blob/ancestor pins, omit export registration,
omit the existing producer blob pin, and retain failed operation resources.
All eleven violate their intended assertions. The first source checkpoint
missed the first two controls; the revised evidence and cleanup assertions
reject both. Nine basic reachability probes also reached their named states.
Three additional probes record ordered events for the same snapshot: reuse
after prefix failure, consumer adoption after another owner releases, and
provider consumption after another owner releases. These reached 3,653,
15,236, and 246,413 distinct states in 1.22, 1.52, and 2.79 seconds.

Commands, source/config hashes, logs, mutation copies, and traces are under
`/tmp/snapshot-foundations-implementation-20260905/implementer`. Early malformed
scratch probes are recorded as errors, not evidence. The model has a normalized
root already; root omission and requested compression require Go controls.
No completed production validation is claimed by this model checkpoint.

The local-build shape now starts with one ordinary owner of the original
snapshot ancestry. Its first export must attach new content to that continuing
owner. Evidence records present content at adoption and extends that expectation
on export registration. It does not demand missing historical bytes merely to
reuse a usable snapshot. The revised export shape passes 5,185,181 distinct
states in 24.80 seconds; import remains 475,119 states in 4.51 seconds.
The continuing-owner witness reaches provider consumption and producer release
with both new blobs still owned (40,429 states, 1.72 seconds). Omitting the
owner content update violates `OwnerHasRecordedContent` (8,130 states, 1.53
seconds). All prior eleven controls and nine basic probes were repeated on
this source and reached their intended assertions. The single producer does
not establish concurrent export cancellation; the changed Go exclusion and
provider ownership paths require that focused witness.
