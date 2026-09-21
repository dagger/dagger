# Describing and importing cached values

## Data transferred

Describe selected live result rows using their existing persisted value representation, declared types, call frames, dependency references, safe transferable extra digests and snapshot-chain descriptions. Pending unevaluated rows carry their original inputs and no completed snapshot. Assign bundle-local ordinals; process-local result IDs and equivalence-class numbers are not portable identities.

Use the declared-reference visitor to translate object payloads and call references. Opaque user JSON remains opaque.

## Export

The existing `Cache.Close` waits for quiescence before `persistCurrentState`; `snapshotPersistState` is not a ready-made live-export API. Live capture must instead take the selected row/dependency/call metadata under `egraphMu`, retain the ordinary row holds, and copy completed part descriptors under each part's existing synchronization. A part still being written is pending or not ready for that capture; export must not inspect partially written state or wait for a global shutdown. The exact part capture and ownership diff requires review before implementation.

`Cache.CapturePersistedRecord` now copies one registered row into the existing persisted representation while the cache is live. It keeps an ordinary row hold for the capture, rejects active lazy attempts or pending bookkeeping, and encodes unstarted inputs without evaluating them. Completed values release the lazy lock before encoding. Saved envelopes copy without typed decoding. The returned bytes and call frame are owned copies with local IDs. Selecting and holding a complete graph, assigning portable IDs, filtering transferred extras, and capturing its terms remain the next exporter work.

Hold rows and snapshots only for the actual capture and requested chain export. Encoding and chain export run outside the graph lock. Capture the extras known at that point; learning another extra later does not require changing the already captured bundle.

Use the existing extra-digest label `remote-cache` to declare which extras may travel ahead of bytes. The pinned Container.from identity carries this label alongside its unchanged content label. Export includes marked extras and normal recipe identities; ordinary local equivalence and teaching still use every digest. Unmarked extras are omitted from remote metadata. No protobuf or local cache format change is needed for the label.

Use the existing snapshot-chain export API. Export only chains requested by the caller; no extra retention while waiting for a service decision.

## Import

Admit a description as a counted operation into the live cache. Allocate B-local rows, relocate declared references, establish ordinary dependency and resource ownership, and install the existing cache terms/digests. Do not run public schema calls or perform downloads merely to decode a value.

Immutable snapshot metadata carries the supplied chain address into lazy output acquisition. Mutable mirror/cache-volume backing remains local and uses the existing resource construction rules. Ordinary cache lookup then sees the imported rows without a parallel lookup system.

The insertion must support persistence and restart using B-local references. Errors unwind the ownership acquired by that insertion through existing cache mechanisms. No per-read conditions, value-evidence tables or first-handle byte validators are added.

## Checks

Use two live caches with deliberately different row IDs. Transfer an object graph, call through the ordinary cache, inspect actual returned fields, and save/restart it on B. Assert no filesystem download occurs at admission or a metadata-only lookup. Exercise real insertion failure and cleanup where the implementation changes those paths.

Status: implement with Lazy operation acquisition so imported metadata has a concrete demand path. The remote service is outside this scope.
