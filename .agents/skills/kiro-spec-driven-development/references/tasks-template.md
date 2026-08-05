# tasks.md — structure and annotated example

## Skeleton

```markdown
# Implementation Plan

- [ ] 1. <Top-level task: a coherent unit of work>
  - [ ] 1.1 <Sub-task: a single focused coding step>
    - <what to do, files to touch, expected outcome>
    - _Requirements: 1.1, 1.2_
  - [ ] 1.2 <Sub-task>
    - _Requirements: 1.3_

- [ ] 2. <Next unit>
  - [ ] 2.1 ...
    - _Requirements: 2.1_

- [ ] 3. Checkpoint: build, lints, and unit tests green
  - <commands to run; what "green" means here>

- [ ] 4. Property test: Property 5 — routing-config state machine
  - Implement as a property-based test, ≥100 iterations, against a reference model.
  - Tag: `// Feature: <feature>, Property 5: <text>`
  - _Requirements: 3.1, 3.2, 3.3, 3.7, 3.8, 4.1, 4.2, 4.4, 4.8_

## Task Dependency Graph
<Machine-readable ordering: which tasks unblock which. JSON or an explicit list.>

## Notes
<Assumptions, sequencing rationale, anything an implementer needs that does not fit a
task line.>
```

## Authoring rules

- **Coding tasks only.** No deploy, marketing, or manual-QA steps. Each task is something
  an agent or engineer writes/edits code for.
- **Dependency order.** Storage/data → core logic → runtime/wiring → edge/API →
  projection/read models → cleanup/compatibility/matrix → integration. Never schedule a
  consumer before its dependency.
- **Every property → a required PBT task.** For each `Property N` in the design, create a
  task that implements it as a property-based test with a minimum iteration count and a
  tag identifying the property. These are required, not optional.
- **Cite requirements.** Each task ends with `_Requirements: X.Y_` so traceability holds.
- **Checkpoints.** After each coherent group, insert a checkpoint task that verifies the
  build compiles, lints pass, and the relevant tests are green. This keeps the plan
  executable incrementally.
- **Prerequisite gate.** Do not write tasks unless both `requirements.md` and `design.md`
  exist. If one is missing, create it first.
- **Dependency graph + notes.** End with the machine-readable ordering and a `## Notes`
  section.

## Bugfix ordering

For bugfix specs, Task 1 is always the **bug-condition exploration property test** —
written to *fail* on the unfixed code (the failure confirms the bug). Subsequent tasks
implement the fix and the preservation/correction properties. See `bugfix-workflow.md`.

## Annotated example (abridged)

```markdown
- [ ] 4. Worker-deployment registry state machine (runtime, pure)
  - [ ] 4.1 Add the routing-config transition functions
    - set_current_version / set_ramping_version with the unset-on-promote side effect,
      conflict-token CAS, and revision bump, in the runtime registry module.
    - _Requirements: 3.1, 3.2, 3.3, 4.1, 4.2, 4.4_
  - [ ] 4.2 Map registry errors to the edge error type
    - _Requirements: 3.4, 4.5, 7.6_

- [ ] 5. Checkpoint: runtime compiles, clippy clean, registry unit tests green

- [ ] 6. Property test: Property 5 — routing-config state machine
  - Reference-model PBT over generated operation sequences, ≥100 iterations.
  - Tag: `// Feature: worker-deployments, Property 5: routing-config state machine`
  - _Requirements: 3.1, 3.2, 3.3, 3.7, 3.8, 4.1, 4.2, 4.4, 4.8_
```

Why this is good:
- Pure logic (4.1) precedes its error wiring (4.2) precedes its property test (6); the
  checkpoint (5) gives a verifiable stopping point.
- The property task references the same requirement set as the design property — the
  chain requirement → property → task is unbroken and auditable.
