# Ground-truth verification discipline

The single most important habit in this workflow: **every behavioural claim is verified
against an authoritative source before it is written into a spec.** A spec that
contradicts the ground truth is the thing that is wrong — fix the spec.

## Why

Memory, doc comments, blog posts, SDK guides, and other agents are all unreliable about
exact behaviour (error codes, defaults, lifecycle ordering, inheritance rules). They have
been wrong in both directions. Relaying an unverified claim and then "fixing" the spec to
match it propagates the error into design, tasks, tests, and code.

## The source hierarchy

Resolve any behaviour question in this order:

1. **The contract/wire shape** — the authoritative schema for messages, fields, field
   numbers, enums, oneofs (e.g. vendored protobuf, OpenAPI, a published schema). Never
   trust generated/derived artifacts that can be stale; read the source schema.
2. **The targeted system's source at the matching version/tag** — for behaviour the
   schema does not specify: defaulting, error mapping, lifecycle ordering, side effects.
   Read the actual code at the pinned tag, not a moving branch, not memory, not docs.
3. **The project's own conventions** (steering/AGENTS files) — for how to express and
   cite the above.

For a Tokeira-style "match system X at version V" project: read X's protos for shape and
X's source at tag V for behaviour. Where your mechanism has no exact analog, the test of
correctness is: *does our response match what X@V would return for the same input?*

## How to verify (offline, pinned, grep-able)

- Use a **local checkout at the exact tag**. Read tagged files directly:
  `git show {tag}:{path}` and `git grep {pattern} {tag} -- {path}`. This is faster,
  pinned, and searchable. Prefer it over web search when a local checkout exists.
- Read the **actual code path**, end to end — handler → client → state machine — not
  just the function whose name matches. Errors are often mapped a layer away from where
  they are raised.
- Dump large source files to a scratch location to read them, then **delete the scratch
  files** when done (they are not part of the spec).

## Citation rules

- Cite by **repo-relative path + version/tag**, e.g.
  `service/frontend/workflow_handler.go @ v1.31.0`, optionally with a pinned-tag URL.
- **Never** put an absolute developer-machine path in a committed spec, doc, or code.
- Cite inline wherever a behaviour decision is non-obvious, so a reviewer can re-check
  against the same ground truth.
- In `requirements.md`, the **Evidence From Current Code** section is the anchor: list
  the authoritative behaviour source and the current-code locations there.

## Handling review findings

When a reviewer or another agent claims the spec contradicts the target:

1. **Verify the finding against ground truth first.** Do not edit on the strength of the
   claim alone.
2. If the finding is **correct**, list the exact source anchor and the precise edits to
   requirements, design, and the affected properties/tasks. Keep all three in sync.
3. If the finding is **wrong**, say so plainly, show the source that refutes it, and do
   not propagate it.
4. Watch for **internal contradictions** the finding exposes (e.g. a field-policy row
   that disagrees with a numbered criterion). Fix those even when the external claim is
   only partly right.

## Target full behaviour

Match the targeted system's full `Implemented` behaviour. Return
`UNIMPLEMENTED`/"unsupported" **only** when the targeted system itself does so for that
exact case — and when you do, state it explicitly and cite the source that proves the
target returns it. "Subset" targets and invented back-compat behaviour the target does
not provide are both wrong.

## Pre-write checklist

- [ ] Is this claim about behaviour (codes, defaults, ordering, side effects)? If yes, it
      needs a source.
- [ ] Did I read the schema for shape and the tagged source for behaviour — not memory?
- [ ] Is the citation a path + tag, with no absolute machine path?
- [ ] If a finding drove this change, did I verify the finding before applying it?
- [ ] Did I clean up any scratch files I dumped to read source?
