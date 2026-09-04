# Engine client lifecycle TLA+ model

ClientLifecycle.tla models per-client request, transport, child, service, and
shared-work leases; live-session runtime reclamation; authoritative session
teardown; lifecycle-bound client metric draining; and the final session
trace/log telemetry barrier.

NestedClientProxy.tla refines the transition immediately before a child enters
that lifecycle. It keeps executable scope authority, context metadata, logical
client IDs, and the exec bootstrap attachables carrier distinct. Its clean
configuration checks exact routing and independent one-shot transports. Its
mutation configurations deliberately choose a metadata parent, cross-scope
carrier, malformed-header fallback, closed-ID rebind, client substitution,
retryable closed response, or close-all behavior; each must violate its named
invariant so the safety gates cannot pass vacuously.

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
