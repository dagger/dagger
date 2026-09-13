# Retaining filesystem producers

## Concrete missing behavior

A completed Directory or File can currently persist only its snapshot identity and selected path. Its restored lazy operation opens that local snapshot. On another engine, a missing snapshot needs the original computation after a remote download fails. A completed Container can likewise lose the original producer once all its parts have finished.

Keep the existing immutable producer inputs independently of whether its outputs have completed. Save those inputs together with completed output metadata. This implements the original-computation requirement Erik described at timeline order 5209 on September 10. Retaining these inputs does not change local restore: a completed local object still opens its saved snapshot, and a missing local snapshot without a remote description keeps its existing error.

| Existing code | Concrete change needed |
| --- | --- |
| `ContainerExecState` in `core/container_exec.go` | Preserve original `Parent`, `Opts`, `ExecMD` and `ModuleContext`; execution mutates `ExecMD`. |
| `Container.EncodePersistedObject` in `core/container_persistence.go` | It currently writes `LazyJSON` only when a part is pending. Retain original inputs after completion for remote fallback. |
| `Directory.EncodePersistedObject` and `File.EncodePersistedObject` | Their completed-snapshot branches return before saving `LazyKind`/`LazyJSON`. Preserve the existing producing lazy operation separately from snapshot restoration. |
| `ContainerRestoreLazy`, `DirectoryRestoreLazy`, `FileRestoreLazy` | Keep local snapshot opening unchanged. Remote-described pending parts may use the original producer, including `ContainerExecLazy`, after a failed download. |
| `newChangesetFromMerge` in `core/changeset.go` | Replace `changeset_merge_output`, whose synthetic inputs are only snapshot ID and path, with an ordinary Directory-producing field. |

## Implementation

Inspect each existing lazy producer's saved fields and preserve the original inputs it already needs. Use declared result references and ordinary dependencies. Do not reconstruct a producer from its preferred digest or from an unrelated later call.

Container exec mutates execution metadata while running. Save an independent copy of its original configuration before those mutations. Preserve the existing exec output group and delegation rules.

The merged Changeset After Directory currently uses a synthetic call. Replace that source with a named internal Directory producer carrying the receiver Changeset, other Changeset result references and conflict strategy. For multiple changesets, record the effective filtered, ordered inputs. Run the existing synchronous merge body inside that field; preserve error timing and ordinary cache behavior. A downstream File selection then has an ordinary root computation. Ship this change separately from Container input retention.

Keep the operation-specific behavior of existing HTTP, Git, image, file and directory producers. For an HTTP resource whose body is missing, a conditional 304 response cannot recreate the body; fallback must be able to fetch it without those validators. Do not introduce a global response-coherence policy.

## Checks

Verify completed producer metadata survives saving and restoration, including original exec options after mutation. Remove the local output and establish that the original operation can supply it through the same demand path. Check the Changeset output through ordinary field evaluation and a downstream selected File. Keep metadata-only reads lazy.

Status: implementation follows the cleaned codec foundation. This work supplies original computations for remote misses; it does not add read records, proof-carrying construction or LLM/MCP producers.
