# Restore / Rewind / Checkpoint: undo for the agent workspace

Status: design notes — not yet implemented. Written after a live session hit
both motivating failure modes; see "Motivating incidents" for the concrete
evidence.

## Problem

The sandboxed agent workspace (editor @agent and friends) gives agents
`edit`/`write`/`rm`/`mv` plus read-only review tools (`status`, `diff`), but no
way to *undo* anything. Two consequences:

1. **Some mistakes are unrecoverable.** The base version of a file (what
   `status`/`diff` diff against) is invisible to the agent — the read tools see
   only the current tree. After an accidental `rm`, the pre-image exists
   nowhere the agent can reach.
2. **Some workflows are inexpressible.** Engine-debugging sessions (see the
   Claude Code transcript this grew out of) lean hard on multi-file undo:
   layering debug instrumentation across N files and stripping it all later,
   and the gold-standard *revert-fix → run test → confirm it fails → re-apply*
   validation dance (`git diff > fix.patch` / `git apply -R fix.patch` in
   bash-land). Without an undo primitive, a bash-less agent must reconstruct
   reverse edits by hand — error-prone exactly when precision matters most.

### Motivating incidents (from a real session)

- **Accidental delete, no way back.** A stray file appeared in pending changes
  after a workspace re-bind; the agent `rm`'d it assuming it was untracked
  noise. It was in HEAD, so the rm became a tracked deletion — and the content
  was unreachable (the agent had incidentally read it earlier, but
  reconstructing 1784 lines from chunked, line-number-prefixed reads is a dare,
  not a recovery path). Needed: `restore(path)`.
- **Botched edit among good edits.** An `edit` anchor accidentally swallowed a
  function signature in a file that already carried good pending edits.
  `restore(path)` would have been *wrong* here (it would nuke the good edits
  too); the agent recovered manually only because the harness echoes a diff
  after every edit. Needed: `rewind(path)`.

These are different failure modes; the tools below are layered, not competing
alternatives — though within each layer there are competing designs.

## Design principles (apply to every option)

- **Destructive ops return the diff they removed.** The dropped content lands
  in the agent's context, so nothing is truly lost (worst case: re-`edit` it
  back). Also self-verifying — the agent sees immediately if it undid the
  wrong thing.
- **"Base" means the `status`/`diff` base, exactly.** Invariant: after
  `restore(path)`, the path disappears from `status`. The base can move
  mid-session (user commits + re-binds); consistency with `status` is what
  keeps that survivable.
- **One mutating tool call = one undo step.** Journal granularity matches the
  agent's own action granularity; `rewind` should never split or merge the
  agent's operations.
- **Cheap by construction.** The workspace is already immutable Dagger
  `Directory` snapshots (copy-on-write, content-addressed). Retaining the base
  ID plus a ring of pre-call snapshot IDs is nearly free; restore/rewind are
  overlay ops plus the diff computation `status`/`diff` already perform.

## Layer 1: per-file undo

### Option 1A: `restore(path)` — drop pending changes to a path

Reset `path` to the base. Returns the dropped diff.

- Covers: accidental `rm`, hopeless mangling, "start this file over".
- Requires: base snapshot only. No journal. Semantics can't surprise anyone
  (`git restore <path>` mental model).
- Limitation: all-or-nothing per file; wrong tool when a file mixes good and
  bad edits.

### Option 1B: `rewind(path, steps=1)` — undo the most recent change(s)

Pop the last `steps` mutating tool calls' effect on `path`. Returns the undone
diff. Repeatable to walk further back. Natural companion: `history(path)`
listing (tool call, op kind, ±lines) per step.

- Covers: the *most frequent* failure mode — my latest edit was wrong, my
  earlier edits were fine. Also un-deletes after `rm` (the rm is just the last
  journaled op).
- Requires: a per-(call, path) journal from workspace-bind time. Changes that
  predate the bind (inherited pending changes) have no journal entries, so
  rewind-to-bottom ≠ restore unless the journal is seeded with the bind-time
  delta.
- Open question: ops that touch many paths at once (a `generate` run, a big
  `mv`). Per the user-facing contract ("undoes the most recent changes to
  *the file*"), `rewind(path)` undoes only that path's part of the call —
  breaking call-atomicity deliberately. Alternative: `rewindCall(callId)`
  undoes the whole call atomically; both can share the journal.

### Option 1C (minimalist fallback): `readBase(path)`

Read-only access to the base version. Strictly weaker — recovery becomes
"read base, re-`write` it" — but it's the smallest primitive that makes the
rm case recoverable at all, and it's independently useful ("what did this
function look like before my changes?"). Cheap enough to add regardless of
1A/1B.

## Layer 2: multi-file undo (the transcript workflows)

Per-file tools can't express "back out the instrumentation across 5 files" or
"temporarily revert the fix, run the test, re-apply". Two competing designs:

### Option 2A: workspace checkpoints

- `checkpoint(label) -> checkpointId` — snapshot the whole workspace.
- `restoreTo(checkpointId, paths?: [...])` — reset the workspace (or a path
  subset) to the checkpoint. Returns the diff it applied.
- `diffSince(checkpointId)` — review what changed since.
- Auto-checkpoints: the harness can mint one before every mutating call (the
  journal from 1B *is* this), making explicit `checkpoint` mostly an act of
  naming.

Pros: dead-simple mental model ("save game / load game"); trivially correct;
`restoreTo` with a path subset subsumes `restore(path)`. Cons: linear —
restoring to a checkpoint discards everything after it, so it can't express
"undo the middle change but keep the later ones"; the revert-to-verify dance
needs a *second* checkpoint (checkpoint → revert → test → restoreTo) which
works but reads backwards.

### Option 2B: patch objects (stash / apply)

- `stash(paths?: [...]) -> patchId` — capture pending changes (optionally
  scoped) as a named patch **and remove them from the tree**. Returns the
  patch text.
- `applyPatch(patchId | literal patch, reverse: bool)` — (re-)apply, either
  direction. Fails loudly on conflict.
- `restore(path)` falls out as `stash(paths: [path])` with the result
  discarded.

Pros: a near-verbatim port of what the bash sessions actually do
(`git diff > fix.patch`, `git apply -R`); expresses non-linear undo (pop the
instrumentation patch, keep the fix); revert-to-verify is literally
`applyPatch(fix, reverse: true)` → test → `applyPatch(fix)`; patches are
inspectable text, which suits the agent's diff-centric context. Cons: patch
application can conflict (needs a clear failure mode: apply nothing, report
rejects); two-step mental model is heavier than checkpoints; agents must
remember to stash *before* the state diverges (checkpoints' auto-mint has no
analog — though "stash = diff since auto-checkpoint, as an object" bridges the
two).

### Option 2C: both, thin

`applyPatch` alone (taking a literal unified diff, `reverse` flag) is worth
having even in the checkpoint world: every restore/rewind/`diff` output is a
patch, so a single apply tool turns *any* diff the agent has ever seen in its
context into a mechanical redo/undo. Small surface, huge leverage, no storage
model at all.

## Recommendation

Phased, smallest-irreversibility-first:

1. **`restore(path)` now** (1A). It fixes the only *unrecoverable* case, needs
   no journal, and its semantics are obvious. Include the dropped-diff return
   from day one.
2. **`rewind(path)` + `history(path)` next** (1B). Highest-frequency
   convenience; requires the per-call journal, which is also the substrate for
   auto-checkpoints later. Decide the multi-path-call question here
   (recommend: per-path rewind, add `rewindCall` only if demanded).
3. **Layer 2: patch objects (2B), with `applyPatch` accepting literal diffs
   (2C).** Recommended over checkpoints because the target workflows
   (instrumentation strip-out, revert-to-verify) are exactly patch workflows,
   patches compose non-linearly where checkpoints can't, and the
   literal-diff `applyPatch` multiplies the value of every diff already flowing
   through the agent's context. Revisit checkpoints (2A) only if patch
   conflicts prove common in practice — they shouldn't, since the agent
   controls both sides of every patch.

## Open questions

- **Base movement:** when the user commits mid-session and the workspace
  re-binds, does the journal survive? (Recommend: journal resets with the
  base, matching `status`; a warning entry in `history` marks the seam.)
- **`generate`/`check` writes:** do harness-initiated mutations (generators
  applying changes) get journal entries? (Recommend: yes, attributed to the
  generator call — otherwise `rewind` has blind spots exactly where surprises
  come from.)
- **Retention:** ring size for auto-snapshots / stashes; content-addressing
  makes storage cheap but the listing should stay skimmable.
- **Naming:** `restore` vs `revert`; `stash` implies git's "put it back
  later" — if 2B's remove-from-tree default proves surprising, offer
  `snapshotPatch` (capture without removing) alongside.
