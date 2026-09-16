# Re-creatable values are lazy values

Directory, File and Container keep their `Lazy` operation after evaluation. The operation's existing run-once state reports completion; a non-nil pointer does not mean work is pending. Whole operations use `IsEvaluated`; named Container groups use `GroupConsumed`. Successful filesystem completion advances the output revision once under the winning body latch, and capture retains its existing exclusion from that body. Output setters advance the revision separately.

Attachment retains the live operation's exact direct inputs as borrowed dependencies, including when evaluation preceded publication. There is no second live operation. Snapshot restore and acquired values retain `lazyKind`/`lazyJSON` bytes (Container needs only `lazyJSON`); opening a completed snapshot does not decode its operation's inputs. A private evaluation decodes fresh run-once state and publishes selected outputs through the ordinary part transaction. It never copies a private latch onto the receiver.

## Construction and call frames

| Field | Construction and first use |
| --- | --- |
| `Query.directory` | Fresh accessors and `DirectoryScratchLazy`, path `/`, empty operation payload. Canonical scratch acquisition happens at first snapshot demand. |
| `Container.withMountedDirectory`, `withMountedFile` | Demand parent metadata before expansion and inherited owner resolution. Copy plain metadata and mount shape into fresh accessors; open no refs during construction. Existing write and delegation groups fill those accessors. A detached source is released on every failure before handoff. |
| `Query._builtinContainer` | Validate the digest and construct `ContainerBuiltinLazy`. Packaged image lookup/import moves to the first required metadata or filesystem part. The existing platform implicit input now participates in the internal call identity. |
| `Query.__schemaJSONFile` | Generate JSON eagerly under the schema input, view and arguments, then retain its bytes in `FileBlobLazy`. Snapshot writing moves to first use. The platform implicit input participates in identity. |
| Unauthenticated HTTP | Keep session-scoped resolution, then select `Query.__httpFile` with the body digest, URL, name, permissions, optional checksum, Last-Modified and platform. The returned File has no persisted HTTPState dependency. |
| Mutable Git refs | Keep lookup/lock behavior, then select `GitRepository.__resolvedRef(name, commit)` on the exact repository. Tree and bundle outputs carry existing typed Lazy operations. Already fixed SHA paths remain direct. |
| Local Git cleaning | Evaluate the unpublished lazy shell immediately to preserve the no-worktree alias decision. Keep the original Directory on that alias path; otherwise retain the evaluated operation. |

All Container config/platform consumers demand metadata before reading it, including direct Directory/File selection, cache-owner dynamic inputs and both legacy service branches. Builtin metadata and filesystem currently share one body: the cold SDK workflow's following mount therefore forces builtin import. This change does not save builtin I/O on that workflow.

HTTP first use may select the local URL state without resolving it. While holding its mutex, it matches the recorded digest/checksum and obtains an independent canonical-snapshot pin. Derivation happens after unlocking, with uncanceled pin cleanup. A missing or changed tuple falls back to an unconditional fetch checked against the recorded body digest. Cancellation and other local-store errors remain errors. Outer resolution always releases its temporary output, including when internal selection fails. Authorization/service HTTP retains its existing coverage boundary.

## Acquisition and persistence

The [part acquisition path](remote-cache-acquisition.md) routes from encoded operation data. Directory/File require a registered kind and valid payload; Container requires nonempty operation JSON and a field in its closed codec table. A recorded field alone does not create an operation. Metadata transforms keep the existing closed recorded-parent delegation table.

`OperationState` is persisted as `operationState`, with `none`, `pending` and `evaluated`. It describes the saved operation, independently of a receiving engine's missing parts. The compatibility cut is native schema 21, result envelope 5 and value bundle 2. Startup cold-starts older native schemas; import rejects older envelopes and bundles.

Stored scalar/module-object results retain their existing semantics. They have no added Lazy operation or backing-content check. When a result is absent, its ordinary resolver demands the backing parts it actually reads. Pending native forms, complete acquired views, scoped inline ownership and onward capture keep their existing contracts.

The binding design is commit `2cbca74cb354e8f70eeb265e16b4ab92c74e6c07`, blob `96f19f1e79ab04c9cddd6489171cafc6a22d04b4`. The implementation evidence records reader inventory, compatibility, HTTP request counts, exact operation/part observations, ownership and cold/warm/restart controls.
