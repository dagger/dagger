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
violations are reserved for deliberately accepted model findings. One
is tracked today: `resources_restart` reproduces the stored
session-resource requirement set drifting from the true transitive
requirement at import and at decode (see the config header). The
`expectedOutcome` map is the authoritative list; every other
configuration is a green regression gate. (The previous finding,
`decode_cancel` — a decode leader's own cancellation failing its parked
joiners — was fixed by making the joiners retry a departed leader's
cancellation and by retrying the post-install lease sync on the next
demand; the configuration now holds that contract green.)

Run the check:

```sh
dagger check tla-check:cache-lifecycle
```

This runs every configuration in parallel and compares each outcome to
the `expectedOutcome` map in `.dagger/modules/tla-check/main.go` (a new
config must be added to that map). On failure, the message names the
configuration and how its outcome diverged from the expectation.
