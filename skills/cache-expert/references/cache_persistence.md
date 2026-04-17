# DagQL Cache Persistence

This document describes the current persistence model for the `dagql` cache.

The source of truth is the code, mainly:

- `dagql/cache.go`
- `dagql/cache_persistence_import.go`
- `dagql/cache_persistence_worker.go`
- `dagql/cache_persistence_self.go`
- `dagql/cache_persistence_resolver.go`
- `dagql/persistdb/schema.sql`
- `core/persisted_object.go`

This doc is about the persistence model itself: what is persisted, when it is
persisted, how objects encode themselves, and what guarantees we do and do not
make.

## The Big Picture

The persistence model is intentionally simple and intentionally best effort.

The cache is fundamentally an **in-memory cache**.

While the engine is running:

- the live cache is in memory
- lookups, publication, ownership, pruning decisions, and lazy evaluation all
  operate against in-memory state
- we do **not** continuously stream cache mutations to disk

Disk persistence is only used as a startup/shutdown checkpoint:

1. on startup, load a previously persisted cache snapshot if it is considered
   valid
2. run the engine entirely from in-memory state
3. on graceful shutdown, serialize the current retained cache state back to disk

This is not meant to behave like a database with durability guarantees. If the
engine crashes or is killed ungracefully, losing the cache is acceptable. It is
just a cache.

## Guarantees And Non-Goals

### What We Intentionally Guarantee

- graceful shutdown attempts to flush the retained cache state to disk
- graceful startup attempts to load that snapshot back into memory
- persistence mirrors the current in-memory graph and metadata closely rather
  than inventing a second looser model
- if persistence is valid, restart can reuse prior dagql cache state and
  snapshot ownership metadata

### What We Intentionally Do Not Guarantee

- crash safety
- durability across ungraceful shutdown
- incremental writes during runtime
- robustness against partially corrupted or semantically inconsistent persisted
  state
- engine-independent object identities

Right now, if persistence is suspect, we do not try to salvage pieces of it. We
wipe it and cold-start.

## Lifecycle

## 1. Startup

`dagql.NewCache` is the entry point.

If no DB path is configured, the cache is just in-memory and persistence is
effectively disabled.

If a DB path is configured, startup does this:

1. open the SQLite DB
2. ensure the schema exists
3. check `meta.schema_version`
4. check `meta.clean_shutdown`
5. if schema version mismatches, wipe the DB and cold-start
6. if the previous shutdown was not marked clean, wipe the DB and cold-start
7. try to import persisted state
8. if import fails, wipe the DB and cold-start
9. record the current schema version
10. mark `clean_shutdown=0`

That `clean_shutdown=0` write at startup is important: it means the store is
considered dirty until a later successful graceful close explicitly marks it
clean.

## 2. Runtime

During normal engine execution:

- the cache lives in memory
- no steady-state persistence writes happen
- the only normal persistence metadata writes are the startup `clean_shutdown=0`
  and the shutdown `clean_shutdown=1`

The runtime cache state may change constantly, but none of that is pushed to
SQLite during normal operation.

## 3. Graceful Shutdown

The engine shutdown path matters here.

`engine/server/server.go:GracefulStop` does the important sequencing:

1. mark the server as gracefully stopping
2. remove all Dagger sessions
3. optionally prune the dagql cache using the normal prune policies
4. close the dagql cache, which persists current state
5. only after successful persistence mark `clean_shutdown=1`

The session removal part is critical. Before persistence, the engine tries to
get rid of session-owned state first so the retained graph is in a steady state.

That means:

- services are stopped
- telemetry/client cleanup runs
- dagql in-flight activity is drained for the session
- `ReleaseSession` removes session ownership edges from the cache

By the time `Cache.Close()` persists, the cache should reflect the post-session
retained state rather than some partially attached session state.

One subtle but important detail: graceful shutdown may still prune before
persistence. So "everything marked persistable gets written" is not quite the
whole story. More precisely:

- session-owned state is released first
- the remaining persisted-edge-retained graph is what is eligible for shutdown
  persistence
- then shutdown prune may still remove some of that retained graph according to
  policy before the final flush

## Best-Effort Failure Handling

The failure strategy is intentionally blunt.

### On Startup

If any of these happen:

- schema mismatch
- `clean_shutdown != 1`
- import failure

the persistence DB is wiped and the engine starts from an empty cache.

### On Shutdown

If persistence fails during `Cache.Close()`:

- the error is logged
- `clean_shutdown=1` is **not** recorded
- DB handles are still closed

Then on the next startup, the store is seen as unclean and wiped.

We do not try to preserve partial progress or repair a half-written store.

## SQLite Store

The persistence store is SQLite via `modernc.org/sqlite`.

The DB is opened with pragmas chosen explicitly for cache semantics rather than
database durability:

- `journal_mode=WAL`
- `busy_timeout=10000`
- `synchronous=OFF`
- `BEGIN IMMEDIATE` transactions

The important implication is that we intentionally choose better performance
over robust crash durability. That matches the "cache, not database" model.

## On-Disk Schema Overview

The schema lives in `dagql/persistdb/schema.sql`.

There are three broad groups of data:

### 1. Meta

- `meta`

Currently used for:

- `schema_version`
- `clean_shutdown`

### 2. Mirrored dagql cache graph/state

- `results`
- `eq_classes`
- `eq_class_digests`
- `terms`
- `term_inputs`
- `result_output_eq_classes`
- `result_deps`
- `persisted_edges`
- `result_snapshot_links`

This is the persisted mirror of the in-memory dagql cache/e-graph state.

### 3. Snapshot-manager persistent metadata

- `snapshot_content_links`
- `imported_layer_blob_index`
- `imported_layer_diff_index`

These do not describe the dagql graph directly. They mirror auxiliary snapshot
manager metadata needed to reconstruct snapshot/content relationships and
imported-layer indexes on restart.

## What Is Actually Persisted

On graceful shutdown, the cache snapshots and writes:

- all live `sharedResult`s in `resultsByID`
- all live terms
- all live eq-classes and their digests
- result-to-output-eq-class associations
- exact result dependency edges
- persisted root edges
- result snapshot ownership links
- snapshot manager persistent metadata rows

That is important: the store does not just save a small set of "roots." It
saves the live retained cache graph and the metadata needed to reconstruct it.

In other words, persistence is trying to serialize the current cache state, not
just enough information to replay everything later.

## What Is Not Persisted

Not everything in the cache is persisted.

Important omissions:

- in-flight `ongoingCall`s
- per-session tracking state
- per-session lazy span state
- arbitrary in-memory cache entries from `cache_arbitrary.go`

Those are runtime-only.

The persisted store is about retained dagql call-cache state and snapshot
metadata, not every transient runtime structure.

## Persistable Roots

The main user-visible way something survives beyond a session is through
`IsPersistable`.

At the dagql field-definition level, `Field.IsPersistable()` sets the field spec
to mark results of that field as eligible for persistence.

At execution time, that turns into `CallRequest.IsPersistable`, and the cache
responds by adding a persisted edge for the completed result.

That persisted edge does two things:

1. it keeps the result alive after session release
2. it makes the result eligible to be written as part of the shutdown snapshot

Because retained results also keep their exact result dependencies alive, making
a result persistable retains its transitive dependency closure too.

This is why shutdown persistence naturally includes more than just the root
persistable results: the retained graph includes whatever those roots depend on.

Pruning is the part of the system that decides which persisted edges survive
over time. That deserves its own doc, but it is directly relevant here because
it controls what still exists to flush at shutdown.

## Persisted Self Payloads

The `results` table stores one `self_payload` blob per result.

That blob is not a raw Go serialization of the whole object. It is a structured
`PersistedResultEnvelope` defined in `dagql/cache_persistence_self.go`.

The current envelope kinds are:

- `null`
- `object_self`
- `scalar_json`
- `list`

The envelope also carries:

- result-local metadata like `resultID`
- `typeName`
- `sessionResourceHandle`

The envelope is the generic dagql-level wrapper. Object-specific details live
inside object JSON payloads implemented by the object types themselves.

## Persisted Object Interfaces

There are three main interfaces to know:

### `PersistedObject`

Implemented by typed self payloads that know how to encode themselves directly:

- `EncodePersistedObject(context.Context, PersistedObjectCache) (json.RawMessage, error)`

This is how objects serialize their own internal state to JSON.

### `PersistedObjectDecoder`

Implemented by zero-value object types that know how to reconstruct themselves:

- `DecodePersistedObject(context.Context, *Server, uint64, *ResultCall, json.RawMessage) (Typed, error)`

This is how object payloads are rebuilt on import or first hit.

### `PersistedSnapshotRefLinkProvider`

Implemented by objects that can name the durable snapshots they own:

- `PersistedSnapshotRefLinks() []PersistedSnapshotRefLink`

This is how object payloads expose snapshot ownership links for
`result_snapshot_links`.

## Cross-Object References

Persisted object payloads often refer to other persisted dagql objects.

Those references are encoded through `encodePersistedObjectRef`, which stores the
referenced object's `sharedResultID`.

This is a major current caveat:

- persisted references are engine-local result IDs
- they are not stable, portable, or engine-independent semantic IDs

That is accepted for now. The persistence format is a snapshot of one engine's
cache state, not a portable interchange format.

## Lazy Persistence

The lazy system is separate conceptually, but it matters directly to
persistence.

The core lazy interface includes:

- `Evaluate`
- `AttachDependencies`
- `EncodePersisted`

That last method is the persistence hook.

For objects like `Directory`, `File`, and `Container`, persisted object encoding
often has two broad forms:

- **snapshot form**
  - the object already has a materialized snapshot/accessor value
- **lazy form**
  - the object has not been fully materialized, but it still has a structured
    lazy operation that can be serialized

This is a big design point: laziness does not block persistence as long as the
lazy operation is structurally representable.

If an object has neither:

- a materialized snapshot/value
- nor a serializable lazy op

then persistence returns `ErrPersistStateNotReady`.

Today `Directory` and `File` explicitly do this when they have neither snapshot
nor lazy state available to encode.

That is important because shutdown persistence is all-or-nothing from the point
of view of clean restart. If a persistable retained result cannot be serialized,
the flush fails, `clean_shutdown=1` is not recorded, and the next startup wipes
the store.

## Snapshot Handling

Snapshots are not encoded only implicitly through object JSON.

There are two related persistence mechanisms:

### 1. Result snapshot links

Objects expose `PersistedSnapshotRefLinks()`, and those are written into
`result_snapshot_links`.

These links describe:

- which snapshot ref keys a result owns
- what role each snapshot plays
- optional slot information

Examples:

- a directory snapshot
- a file snapshot
- container rootfs / mount / meta snapshot ownership
- mutable-owner objects like cache volumes and mirrors

### 2. Snapshot-manager persistent metadata

Separately, the snapshot manager exports:

- snapshot-content digest links
- imported-layer indexes by blob digest
- imported-layer indexes by diff ID

Those rows are written into the snapshot metadata tables and loaded back into
the snapshot manager at startup.

## Snapshot Owner Leases On Import

Import does more than just rebuild tables in memory.

After reading the mirrored rows, startup also:

1. loads snapshot-manager persistent metadata
2. computes the desired owner lease IDs implied by the imported retained results
3. re-attaches those owner leases to snapshots
4. deletes stale Dagger-owned owner leases that are no longer desired

This is how persisted dagql ownership is translated back into live containerd
lease ownership at startup.

The ordering matters:

- attach desired leases first
- then delete stale ones

That intentionally biases failure modes toward temporary over-retention rather
than accidental ownership loss.

## Import Behavior

`importPersistedState` rebuilds the live cache in several phases:

1. read all mirrored rows from SQLite
2. rebuild eq-classes
3. rebuild results
4. rebuild persisted edges and increment ownership
5. rebuild terms and term inputs
6. rebuild result-output-eq-class membership
7. rebuild exact dependency edges and increment ownership
8. load result snapshot links
9. recompute required session resources
10. rebuild digest indexes
11. opportunistically decode some persisted payloads eagerly
12. load snapshot-manager metadata and restore owner leases

The opportunistic eager decode is subtle:

- some payloads can be decoded immediately without a live dagql server context
- others, especially object payloads needing object decoders, may remain lazy
  until first use

The implementation detail behind that is important:

- startup import does attempt an eager decode pass
- but that eager pass calls the persisted self codec without a live dagql server
- object decode requires a current dagql server plus object-type lookup
- so object payloads that cannot be reconstructed in that reduced context remain
  as persisted envelopes and are decoded later by
  `ensurePersistedHitValueLoaded`

This matches the current code path and is not just a vague policy choice.

This was added originally with the intention of handling dynamic object
reconstruction, especially around module objects and other schema-dependent
object types. Import-time decode does not necessarily have enough live
schema/type context to rebuild those objects correctly, so the system defers
full decode until the object is accessed through an actual server/resolver
path.

It may be worth reassessing whether that concern is still fully valid in the
current architecture, but today the lazy-on-first-use decode is still real and
intentional in the implementation.

So after import, a result may exist in the graph with:

- a `persistedEnvelope`
- `hasValue == false`

That is valid. `ensurePersistedHitValueLoaded` is the boundary that materializes
that payload before the result escapes to callers.

## Shutdown Write Path

Shutdown persistence is a two-step process:

### 1. Snapshot the in-memory state

`snapshotPersistState` walks the in-memory cache and builds a detached
`persistStateSnapshot`.

This is important for performance and correctness:

- it holds `egraphMu` only while copying the graph state out
- it releases the lock before doing the expensive JSON envelope encoding and SQL
  writes

So the live cache is not held under the graph lock for the whole flush.

### 2. Apply the snapshot to SQLite

`applyPersistStateSnapshot` then:

1. starts a transaction
2. clears all mirror tables
3. inserts the new rows
4. commits

This is a whole-snapshot rewrite, not an incremental update.

That is another deliberate simplification:

- no change-by-change persistence logic
- no merge logic against prior on-disk state
- just replace the whole mirrored cache snapshot in one transaction

## Result Serialization Details

For each result being flushed, persistence stores:

- `call_frame_json`
- `self_payload`
- expiration / time metadata
- record type / description

It also stores separately:

- exact result deps
- snapshot links
- output eq-class membership

The authoritative call frame matters a lot. It is the semantic identity and
reconstruction anchor used during later decode.

## Current Limitations And Sharp Edges

### 1. Engine-local result IDs

Persisted object references are keyed by `sharedResultID`, which is only
meaningful inside one engine cache snapshot.

### 2. Graceful-shutdown only

If shutdown is not clean, the store is wiped on the next startup.

### 3. All-or-nothing tolerance

If import or startup validation fails, the store is wiped.
If shutdown flush fails, the next startup wipes the store.

### 4. Some object forms are still unsupported

One explicit example today: `Container.EncodePersistedObject` still rejects
containers carrying:

- services
- secrets
- sockets

That is a known first-cut restriction.

### 5. Arbitrary cache entries are not part of persistence

The arbitrary in-memory cache is session/runtime-only today.

## Performance Considerations

The persistence model makes a few strong performance choices:

- no runtime mutation writes to SQLite
- SQLite opened with `synchronous=OFF`
- whole-snapshot rewrite on shutdown instead of fine-grained updates
- graph lock held only for snapshot extraction, not for SQL writes
- startup import reconstructs in-memory indexes directly instead of replaying the
  whole call graph through normal execution paths

The price paid is lower durability and a willingness to wipe the store if
anything looks wrong.

## Suggested Reading Order

If you want to understand the live implementation quickly, this order works
well:

1. `dagql/cache.go`
   - `NewCache`
   - `prepareCacheDBs`
   - `ReleaseSession`
   - `Close`

2. `engine/server/server.go`
   - `GracefulStop`

3. `dagql/cache_persistence_worker.go`
   - `persistCurrentState`
   - `snapshotPersistState`
   - `applyPersistStateSnapshot`
   - `persistResultEnvelope`

4. `dagql/cache_persistence_import.go`
   - `importPersistedState`
   - `ensurePersistedHitValueLoaded`

5. `dagql/cache_persistence_self.go`
   - `PersistedResultEnvelope`
   - `PersistedObject`
   - `PersistedObjectDecoder`
   - `PersistedSnapshotRefLinkProvider`
   - `encodePersistedResultEnvelope`
   - `decodePersistedResultEnvelope`

6. `dagql/persistdb/schema.sql`
   - table layout

7. `core/persisted_object.go`
   - cross-object reference helpers

## Short Summary

The current dagql persistence model is a best-effort startup/shutdown snapshot
of the live in-memory cache: load once on startup, run entirely in memory,
flush once on graceful shutdown, and wipe the whole store whenever the on-disk
state looks unsafe or inconsistent.
