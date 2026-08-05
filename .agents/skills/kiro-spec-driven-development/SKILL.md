---
name: kiro-spec-driven-development
description: Author and review Kiro-style spec-driven development documentation — requirements.md (EARS acceptance criteria), design.md (architecture plus executable correctness properties), and tasks.md (property-based-test-backed implementation plans) under .kiro/specs/{feature}/. Use when creating a new feature spec, fixing a bug via the bug-condition method, refining requirements into testable form, deriving a design from requirements (or vice versa), breaking a design into an ordered task list, or verifying spec claims against ground truth. Covers requirements-first, design-first, and bugfix workflows, EARS syntax, ground-truth verification discipline, and property-based testing integration.
---

## What this skill produces

Spec-driven development turns a rough idea into three reviewed, ground-truthed
documents that live in `.kiro/specs/{feature-name}/`:

1. `requirements.md` — user stories with **EARS-format** acceptance criteria, a glossary,
   a target state, evidence from the current code, and (where a wire/field contract
   exists) a field-by-field policy table.
2. `design.md` — architecture, components, data models, and a set of **executable
   correctness properties**, each tracing back to the requirements it validates.
3. `tasks.md` — an ordered, dependency-aware implementation plan where every property
   becomes a **required property-based test (PBT)** task.

The workflow is **iterative and consent-gated**: you establish a ground truth, get the
user's agreement on each document, and only then move to the next. Never silently jump
phases.

## Core principles (non-negotiable)

1. **Ground truth over memory.** Every behavioural claim is verified against an
   authoritative source (spec, standard, vendored schema, or the actual source of the
   system being matched) *before* it is written. Do not relay claims from memory, doc
   comments, blog posts, or another agent. See `references/ground-truth-verification.md`.
2. **EARS for every acceptance criterion.** Each criterion uses the EARS keywords
   (WHEN / IF / WHILE / WHERE / THEN / THE / SHALL). No prose-only "shoulds." See
   `references/ears-format.md`.
3. **Account for everything.** When a contract surface exists (proto fields, API
   parameters, config keys, enum variants), every element is accounted for in a policy
   table — target behaviour, error on invalid input, and persistence/side-effect impact.
4. **Properties are first-class and required.** Correctness properties are derived in the
   design and become mandatory PBT tasks. They are never "optional" or "nice to have."
   See `references/property-based-testing.md`.
5. **Target `Implemented`, not a subset.** Match the full targeted behaviour. Only return
   `UNIMPLEMENTED`/"unsupported" when the targeted system *itself* does for that case,
   and say so explicitly with a citation.
6. **Traceability.** Design properties cite the requirements they validate
   (`**Validates: Requirements X.Y, X.Z**`); tasks cite the requirements they implement.
   The chain requirement → property → task is unbroken.
7. **Consent gates.** Finish a document, summarise it in user-facing terms, then wait for
   approval before advancing. Do not reveal internal counts, file paths, or that a
   workflow is being followed when operating as Kiro itself; when operating as a skill,
   be explicit and transparent.

## Step 0 — Select the workflow

Decide the spec type and entry point before creating any file:

- **Feature, requirements-first** (default): Requirements → Design → Tasks. Use when
  business needs are clearer than the technical approach.
- **Feature, design-first**: Design → Requirements → Tasks. Use when the technical
  approach is clear and requirements need to be formalised from it.
- **Bugfix**: always requirements-first, using the **bug-condition method**. Use when
  something that should work does not. See `references/bugfix-workflow.md`.
- **Quick plan**: auto-generate all three with light clarification, for small,
  well-scoped work.

Indicators: "add / new / create / implement / build / support" → feature;
"fix / bug / crash / broken / regression / doesn't work" → bugfix.

Pick a short **kebab-case** feature name (e.g. `user-authentication`,
`worker-deployments`). The spec directory is `.kiro/specs/{feature-name}/`.

## Step 1 — Requirements (`requirements.md`)

Produce, in order:

1. **Introduction** — what the feature is, its scope boundary, and which sibling work it
   depends on or defers to. State the targeted compatibility/behaviour authority here.
2. **Glossary** — define every domain term you will reuse. Precise definitions prevent
   ambiguous criteria later.
3. **Target State** — the end state in one place: what becomes supported, what stays out
   of scope, and any sanctioned exceptions (with the citation that justifies each).
4. **Evidence From Current Code** — cite where the behaviour lives today (handlers,
   modules, line anchors) and the authoritative source for the *target* behaviour. This
   is the ground-truth anchor reviewers re-check against.
5. **Field / Contract Policy** (when applicable) — one table per request/response or
   contract surface; every field gets a target policy, an error-if-invalid, and a
   persistence/side-effect column.
6. **Requirements** — numbered `### Requirement N: Title`, each with a **User Story**
   (`As a {role}, I want {capability}, so that {benefit}`) and numbered EARS
   **Acceptance Criteria**.

Then **detail each requirement**: make every criterion precise, testable, and atomic.
Split compound criteria. Resolve ambiguity against ground truth, not assumption. Full
structure and an annotated exemplar: `references/requirements-template.md`.

## Step 2 — Design (`design.md`)

Produce:

1. **Overview** — what the design does and the sources its wire-shape and behaviour are
   derived from.
2. **Dependencies and Non-Goals** — owning relationships with sibling specs; explicit
   non-goals so scope does not creep.
3. **Architecture** — prose plus a `mermaid` diagram of the major paths. Distinguish
   control-plane from data/dispatch paths where relevant.
4. **Components and Interfaces** — the concrete modules/traits/functions to add or
   change, with signatures. Respect existing architectural boundaries (e.g. a pure
   core vs. an I/O layer); state where each new piece lives.
5. **Data Models** — durable and in-memory types, each field traced to its contract
   source.
6. **Correctness Properties** — the heart of the design. Each property is a universally
   quantified statement ("*For any* … the system SHALL …") with a
   `**Validates: Requirements X.Y**` line. Deduplicate so each property adds unique
   value. See `references/property-based-testing.md`.
7. **Error Handling** — a table mapping every error condition to its internal error type
   and external status/code.
8. **Testing Strategy** — PBT for the property set, example-based unit tests for fixed
   edge cases, and integration tests for end-to-end paths; state where each lives.

Full structure and an annotated exemplar: `references/design-template.md`.

## Step 3 — Tasks (`tasks.md`)

Produce an ordered checkbox list of coding tasks only (no deploy/marketing tasks):

- Top-level numbered tasks with indented sub-tasks (`- [ ] 1.1 …`).
- Order by dependency: data/storage → core logic → runtime/wiring → edge/API →
  projection/read models → cleanup/compatibility → integration.
- **Every correctness property becomes a required PBT task**, tagged with the property
  it implements and the requirement it validates.
- Insert **checkpoints** after coherent groups so progress is verifiable (build, lints,
  tests green).
- Each task cites the requirement(s) it implements via `_Requirements: X.Y_`.
- End with a **Task Dependency Graph** (the machine-readable ordering) and a `## Notes`
  section.

Validate prerequisites: do not write tasks until both requirements and design exist.
Full structure and an annotated exemplar: `references/tasks-template.md`.

## Bugfix variant

Bugfix specs replace requirements with a **bug-condition** document: formalise the
buggy condition `C(X)`, write an exploration property test that is *expected to fail* on
the unfixed code (failure confirms the bug), then specify the fix as preservation +
correction properties. Details and task-ordering rules: `references/bugfix-workflow.md`.

## Quality gate (check before declaring any document done)

- Every acceptance criterion is in EARS form and is atomic.
- Every contract element is accounted for (no silent gaps).
- Every behavioural claim has a ground-truth citation; none rests on memory.
- Every design property has a `**Validates: Requirements X.Y**` line and is reachable
  from a requirement.
- Every property has a corresponding required PBT task in `tasks.md`.
- Targets full `Implemented` behaviour; any exception is justified by the targeted
  system's own behaviour with a citation.
- Tasks are dependency-ordered, cite requirements, and include checkpoints + a
  dependency graph.
- Document validates clean (run the editor/diagnostics check on each `.md`).

## Interaction protocol

- One document per turn; summarise and request review; wait for the user.
- Incorporate feedback by editing the existing document, not regenerating from scratch
  (preserve manual edits).
- When a finding contradicts the spec, verify it against ground truth *first*. If the
  finding is wrong, say so and do not propagate it. If correct, list the exact source
  anchor and the precise edits.
- Read before you write: read the relevant existing code/spec before asserting how it
  behaves.

## Reference files

- `references/ears-format.md` — EARS keywords, templates, and worked rewrites.
- `references/requirements-template.md` — full requirements.md skeleton + annotated example.
- `references/design-template.md` — full design.md skeleton + annotated example.
- `references/tasks-template.md` — full tasks.md skeleton, checkpoints, dependency graph.
- `references/property-based-testing.md` — deriving, writing, and tagging properties/PBTs.
- `references/ground-truth-verification.md` — the verification discipline and citation rules.
- `references/bugfix-workflow.md` — bug-condition method end to end.
