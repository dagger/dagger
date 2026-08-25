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
finding is tracked today: every configuration is a green regression
gate, and the `expectedOutcome` map is the authoritative list. (The two
most recently closed findings: `decode_cancel` — a decode leader's own
cancellation failing its parked joiners — fixed by retrying a departed
leader's cancellation and the post-install lease sync; and
`resources_restart` — the stored session-resource requirement set
drifting from the true transitive requirement — fixed by recomputing
dependency-first at import and leaving the stored set alone at decode
install. Both configurations now hold their contracts green.)

Run the check:

```sh
dagger check tla-check:cache-lifecycle
```

This runs every configuration in parallel and compares each outcome to
the `expectedOutcome` map in `.dagger/modules/tla-check/main.go` (a new
config must be added to that map). On failure, the message names the
configuration and how its outcome diverged from the expectation.
