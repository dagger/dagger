# Retaining filesystem producers

A receiving engine needs the original computation when a supplied remote download cannot provide an output. The saved producer uses the original operation and inputs, as Erik requested at timeline order 5209. It is independent of whether the local output has completed.

## Implemented

| Source | Behavior |
| --- | --- |
| `ContainerExecState` in `core/container_exec.go` | Keeps a private copy of original execution metadata. Runtime metadata derivation, overwrite and retry behavior stay unchanged. |
| `Container.EncodePersistedObject` in `core/container_persistence.go` | Retains the completed producing operation locally and its encoded bytes after restoration. Completed-row decode does not load producer ancestors. Pending output groups keep their existing behavior. |
| `Directory` and `File` codecs | Save the producing operation's kind and inputs beside the snapshot identity and path, including container-derived producers. Snapshot-form decode retains raw bytes; the existing reference visitors relocate those inputs. |
| Changeset merge fields in `core/schema/directory.go` | Internal `__mergeWithChangeset` and `__mergeWithChangesets` produce the After Directory through the existing merge body. Public wrappers retain filtered, ordered inputs and ordinary cache behavior. |

The operational lazy pointer still clears on successful completion. A new child retains its own producer; it does not inherit the parent's saved producer. Stored snapshot opening remains unchanged: a missing local snapshot without a remote description returns the existing error.

## Remaining integration

Connect the saved inputs to [remote-described output acquisition](remote-cache-acquisition.md). The saved bytes alone do not implement the download or original-computation fallback. Decode a completed producer only when that demand path actually needs it.

Keep the operation-specific behavior of existing HTTP, Git, image, file and directory producers. For an HTTP resource whose body is missing, a conditional 304 response cannot recreate the body; fallback must be able to fetch it without those validators. Do not introduce a global response-coherence policy.

## Verification

Completed Container, Directory and File producer inputs survive save/restart, path/metadata reads, local snapshot-open failure and retry, and reference relocation without decoding ancestors. Original exec input metadata survives runtime derivation. Changeset merge producers have package, native merge, restart and generator checks. These are producer-retention results; they do not yet establish cross-engine download or fallback.

The acquisition work must exercise actual remote-chain failure followed by the original producer. Local missing-output behavior remains unchanged. No read records, nested observation forwarding or LLM/MCP producer work is included.
