# Workspace save performance

`dagger agent` saves a frozen workspace into the caller's checkout without
reloading the conversation. The save captures the destination, composes its Git
state, integrates the requested changes in engine-owned storage, and applies a
checked update to the host. The optimizations below remove redundant work while
preserving that flow.

## Retained optimizations

- **Local outgoing bundles:** borrow the mounted local object database through
  temporary alternates, write selected refs in private scratch storage, and pack
  before releasing the source. Remove scratch storage before committing the
  output snapshot. Remote repositories retain their existing fetch behavior,
  including annotated tags.
- **Unchanged snapshots:** reuse known trees when a save snapshot has no edits.
  Create transport commits with the required parents without resetting and
  restaging unchanged content. Nonempty snapshots still apply and stage edits.
- **Destination materialization:** materialize the save checkout once and reuse
  it throughout integration instead of repeatedly constructing Git directories.
- **Content-only checkouts:** use shallow checkouts for captured HEAD and
  worktree content. Retain complete history for operations that need ancestry.
- **Owned destination history:** reuse an evaluated local source or previous-save
  base only when it matches the captured HEAD, origin, and push routing. Cache
  the read-only storage and complete object-closure proof by the immutable ref
  result. Unsupported or ambiguous storage uses destination reconstruction.

## Correctness boundaries

Destination capture and checked host writes remain authoritative. The save
uses the caller's explicit path and does not route through the frozen source's
client. Staged and unstaged destination edits, file modes, symlinks, deletions,
and approved saved additions retain their existing semantics. Unrelated host
untracked files remain private. Retrying a save must check file types and modes,
and an unchanged source/comparator pair must remain a no-op.

History reuse requires already-evaluated, self-contained local storage. Shallow
or partial repositories, alternates, replacements, grafts, missing objects,
live workspaces, routing mismatches, and unsupported identities fall back.
Qualification must not trigger evaluation of a lazy host source. Cancellation
remains an error rather than silently becoming a reuse miss.

Bundle import still fetches the exact prerequisite closure into isolated
storage. Source-only objects must not mask an incomplete bundle; temporary
alternates used for outgoing packing must not survive in exported storage.
Readiness-cache keys must distinguish different immutable storage even when
its advertised HEAD is the same.

## Observability and verification

Production spans expose destination capture/composition, local checkout
materialization, checkpoint phases, and sanitized Git subprocess names. Git
argument values and environment values are excluded from subprocess telemetry.
The trace sink and export-scoped operation counts support structural assertions
about reuse and fallback behavior without timing thresholds.

Retained regressions cover full and incremental SHA-1/SHA-256 bundles, packed
and loose objects, source immutability, annotated tags, quoted alternate paths,
missing objects, snapshot classification and transport parents, history,
explicit checkout isolation, retries and captured file kinds, real Git origins,
routing fallbacks, and readiness-cache isolation.

Prerequisite repacking and private-index/full-checkout snapshots were evaluated
and rejected. Investigations of source and previous-save fetch reuse produced
no additional production change. Their test-only implementations, benchmark
runners, sample data, and experimental journal are intentionally omitted.
