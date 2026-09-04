# Engine client lifecycle TLA+ model

ClientLifecycle.tla models per-client request, transport, child, service, and
shared-work leases; live-session runtime reclamation; authoritative session
teardown; lifecycle-bound client metric draining; and the final session
trace/log telemetry barrier.

The detailed DagQL cache algorithm remains in
dagql/tla/CacheLifecycle.tla. This model consumes one contract from it:
successful WaitSessionRelease means deferred cache cleanup and release hooks
have completed.

Each action comment names its intended Go serialization point. Every important
configuration should have a corresponding Go race test; TLC validates the
algorithm's interleavings but does not automatically prove that the Go
implementation refines this model.

Run all client lifecycle configurations with:

    dagger --cloud check tla-check:client-lifecycle
