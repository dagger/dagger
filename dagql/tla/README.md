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
`SharedLeaseReleasedWhenRetired`. One accepted finding is tracked today:
`decode_cancel` reproduces a decode leader's own cancellation failing its
joiners (see the config header); every other configuration is a green
regression gate.

Run the check:

```sh
dagger check tla-check:cache-lifecycle
```

This runs every configuration in parallel and compares each outcome to
the `expectedOutcome` map in `.dagger/modules/tla-check/main.go` (a new
config must be added to that map). On failure, the message names the
configuration and how its outcome diverged from the expectation.
