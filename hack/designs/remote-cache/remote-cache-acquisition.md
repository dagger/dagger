# Acquiring missing filesystem outputs

A learned result is an ordinary cached lazy result with metadata about where its missing output can be downloaded. The remote fallback below applies to parts carrying that description. A local saved snapshot that is missing without a remote description keeps today's error; this work adds no local recomputation fallback.

## Demand path

Within the existing lazy part evaluation:

1. Use its available local snapshot.
2. If supplied remote-chain metadata exists, import that chain through the existing snapshot manager.
3. If it cannot supply the part, run the original lazy producer.

A lookup, metadata read or decode does not pull filesystem contents. A normal cache miss does not synchronously ask a remote service for an offer. Expired or unusable download addresses permit the ordinary computation fallback.

Offers for already-created but unstarted work and refreshing an expired address for known content were discussed in Erik's September 10 answers (order 5077). Preserve them as explicit follow-ups: the concrete update/API behavior must be reviewed before implementation. Interrupting an already-running exec remains deferred. No address-refresh RPC is part of the present engine-only slice.

Retain the existing output groups and delegation. An exec still produces its current group when executed; downloading one supplied part does not force unrelated parts or the full dependency graph to download. Nondeterministic output differences keep the behavior Erik explicitly accepted.

## Ownership and persistence

Attach an imported snapshot using the existing snapshot/result ownership paths. Release an unsuccessful attachment's temporary resources through their existing cleanup. Persist the supplied chain metadata and original producer inputs so a restarted engine can follow the same demand path.

Use the current per-part synchronization. Introduce an additional state transition only when a concrete import/computation race requires it. There is no separate global acquisition coordinator, private replacement-value protocol or read-consistency lifecycle.

Local-only behavior, digest joining, service keys and pruning remain unchanged. The transfer annotation on extra digests belongs at metadata export/import; it is not a new equality rule.

## Checks

Exercise a real local open, a real chain import, and an unavailable-chain fallback that executes the original producer. Verify repeated demand shares the existing lazy operation, cancellation cleans up its existing holds, and restart retains enough metadata to perform the same operation. From-image metadata reads must not unpack layers.

Status: implement through existing lazy evaluation and chain APIs. No public remote service or background acquisition is included.
