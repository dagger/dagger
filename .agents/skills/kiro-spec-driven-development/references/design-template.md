# design.md — structure and annotated example

## Skeleton

```markdown
# Design Document: <Feature>

## Overview
<What the design does. The sources its wire-shape and behaviour are derived from.>

## Dependencies and Non-Goals
### Owning relationships
- <Sibling spec/component> persists/threads X; this design consumes and applies it.
### Non-goals
- <Explicitly out of scope, so scope does not creep.>

## Architecture
<Prose describing the major paths. Distinguish control-plane vs data/dispatch paths.>

```mermaid
flowchart LR
    Client --> Edge --> Core --> Store
```

## Components and Interfaces
### <Layer / module> (`path/to/file`)
<What to add/change, with concrete signatures. Respect existing boundaries — e.g. a
pure core with no I/O vs. an I/O layer. State where each new piece lives.>

```rust
// representative signatures, not full implementations
```

## Data Models
<Durable and in-memory types. Trace each field to its contract source.>

## Correctness Properties
*A property is a statement that holds across all valid executions — the bridge between a
human-readable spec and a machine-checkable guarantee.*

### Property 1: <Name>
*For any* <input space>, <the system SHALL exhibit the invariant/behaviour>.

**Validates: Requirements 1.1, 1.2, 1.6**

### Property N: <Name>
...

## Error Handling
| Condition | Internal error | External status/code |
|---|---|---|
| ...       | ...            | ...                  |

## Testing Strategy
- **Property tests (required):** implement Properties 1..N, ≥100 iterations each.
- **Unit tests (example-based):** fixed edge cases not worth a generator.
- **Integration tests:** end-to-end paths through real layers.
- **Placement:** which crate/module each test set lives in.
```

## Authoring rules

- **Architecture before components.** A diagram plus a paragraph orients the reader; then
  go concrete.
- **Honour existing boundaries.** If the system has a pure deterministic core, keep I/O,
  async, storage, and metrics out of it — place those in the appropriate layer and say
  so. Reuse existing patterns (translation functions, adapters, CAS stores) rather than
  inventing parallel ones.
- **Trace data models to the contract.** Each struct field cites the proto field /
  config key / column it represents.
- **Properties are the centre of gravity.** Derive them from the requirements, make each
  universally quantified ("*For any* …"), deduplicate, and give each a
  `**Validates: Requirements X.Y**` line. See `property-based-testing.md`.
- **Error table is total.** Every error condition in the requirements maps to exactly one
  internal type and one external code.
- **Testing strategy names placement.** Say which module each property/unit/integration
  test lives in, and which test library to use (the workspace standard — do not hand-roll
  property infrastructure).

## Annotated example (abridged)

```markdown
### Property 5: Routing-config state machine

*For any* deployment and any sequence of set-current / set-ramping operations, the
routing config evolves per the targeted rules: setting current to an existing version
sets current_deployment_version, updates the changed-time, and bumps revision_number; an
empty build_id sets current/ramping to nil; setting current to the currently-ramping
version atomically unsets ramping; a ramp percentage in [0,100] sets ramping version,
percentage, and times; a ramping version equal to a non-nil current version is rejected
with FAILED_PRECONDITION; and a successful mutation returns a fresh conflict token plus
the correct deprecated previous_* values.

**Validates: Requirements 3.1, 3.2, 3.3, 3.7, 3.8, 4.1, 4.2, 4.4, 4.8**
```

Why this is good:
- It is universally quantified over a generated input space (sequences of operations),
  which is exactly what a property-based test consumes.
- It encodes a *reference model* (the expected state evolution) rather than a single
  example, so the PBT can compare implementation output to the model.
- It cites every requirement it covers, keeping the requirement → property → task chain
  intact.
- Its behaviour was verified against the targeted source, so the property is correct, not
  merely plausible.

## Design-first note

In the design-first workflow you write this document first, then derive `requirements.md`
from it. The properties still drive the requirements: each property should be expressible
as one or more EARS criteria. Keep the two in sync — a property with no backing
requirement, or a requirement no property validates, is a gap.
