# Engine client lifecycle TLA+ model

ClientLifecycle.tla models per-client request, transport, child, service, and
shared-work leases; live-session runtime reclamation; authoritative session
teardown; lifecycle-bound client metric draining; and the final session
trace/log telemetry barrier.

Green configurations are regression gates. ClientLifecycle_blocking_registration
is a deliberate mutation: it models child registration that blocks behind
session teardown instead of failing fast at the scope serialization point, and
must violate TeardownEventuallyCompletes so that liveness gate cannot pass
vacuously.

The process-level proxy that an exec exposes to nested clients (one exact
one-shot transport per logical client ID, the headerless bootstrap identity,
malformed-header rejection, and terminal responses for closed transports) is a
sequential routing decision made under one mutex. It is covered by Go unit
tests rather than a TLA+ model: see engine/engineutil/executor_nested_test.go
and the nested transport tests in engine/server/session_test.go. The one
concurrency rule the proxy must respect, that registration never blocks
behind session teardown, is what the blocking_registration mutation checks.

The detailed DagQL cache algorithm remains in
dagql/tla/CacheLifecycle.tla. This model consumes one contract from it:
successful WaitSessionRelease means deferred cache cleanup and release hooks
have completed.

Each action comment names its intended Go serialization point. Every important
configuration should have a corresponding Go race test; TLC validates the
algorithm's interleavings but does not automatically prove that the Go
implementation refines these models.

Run all client lifecycle configurations with:

    dagger check --cloud tla-check:client-lifecycle
