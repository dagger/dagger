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
violations are reserved for deliberately accepted model findings. No
such finding is tracked today: every configuration is a green
regression gate, and the `expectedOutcome` map is the authoritative
list. One surfaced finding awaits a ruling and is documented as a
precise exemption inside `NoSpuriousErrors` rather than as a red
configuration: a publisher's own release between fn completion and
attachment fails innocent cross-session readers parked at the attach
barrier (the same defect family as the fixed `decode_cancel` joiner
finding; the analogous fix is a retry/miss classification for readers). (The most recently closed findings: `resources_gated_growth` — a
result's transitive requirement growing after the lookup filter ran —
fixed by re-checking the filter after the attach barrier and by
refusing requirement-carrying deps on explicit retention edges;
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
# expensive otherwise - ~50-60 minutes wall with four TLC JVMs; the
# largest configurations each exceed 40 million distinct states
# (resources_gated_growth ~83M, resources_restart ~73M, lazy_import
# ~47M, persist ~40M)
dagger --env dev check tla-check:cache-lifecycle
```

Every run compares each configuration's outcome to the `expectedOutcome`
map in `.dagger/modules/tla-check/main.go` (a new config must be added
to that map; cheap ones belong in `quickConfigs` too). On failure, the
message names the configuration and how its outcome diverged from the
expectation. Because CI no longer runs any of this, the full suite
before pushing is the only line of defense: do not skip it.
