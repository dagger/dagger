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
