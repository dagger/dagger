# requirements.md — structure and annotated example

## Skeleton

```markdown
# Requirements Document

## Introduction
<What the feature is. Its scope boundary. The compatibility/behaviour authority it is
verified against. Sibling work it depends on or defers to. Whether it is foundational
(new durable state, cross-cutting changes) or contained.>

## Glossary
- **Term:** precise definition reused by the criteria below.
- ...

## Target State
<The end state in one place: what becomes supported; what stays explicitly out of
scope; any sanctioned exceptions, each with the citation that justifies it.>

## Evidence From Current Code
- **Contract shape (authoritative):** where the wire/field/config shape is defined.
- **Behaviour (authoritative):** the source of truth for the target behaviour, by
  path + version/tag.
- **Current handlers / code:** where the behaviour lives today, with line anchors.
- **Dependencies:** sibling specs/components that persist or thread state this feature
  consumes.

## Field Policy   <!-- include when a contract surface exists -->
### <MessageOrSurfaceName>
| Field (id) | Target policy | Error if invalid | Persistence/side-effect impact |
|---|---|---|---|
| ...        | ...           | ...              | ...                            |

## Requirements

### Requirement 1: <Title>
**User Story:** As a {role}, I want {capability}, so that {benefit}.

#### Acceptance Criteria
1. WHEN ... THE ... SHALL ...
2. IF ... THEN THE ... SHALL ...
3. ...

### Requirement N: <Title>
...

## Iteration and Feedback Notes   <!-- optional but recommended -->
- Open questions, tracker corrections, verification status with citations.
```

## Authoring rules

- **Glossary first.** Define terms before using them in criteria; this removes most
  ambiguity downstream.
- **Field policy is exhaustive.** Every field of every in-scope request/response (or
  every config key / enum variant) appears with all three columns filled. A missing row
  is a gap; reviewers will catch it.
- **Each requirement = one user story + numbered EARS criteria.** Number criteria so
  design properties and tasks can cite `Requirements N.M`.
- **Errors are explicit.** State the exact status/code, not "rejects."
- **Cite the authority** for any non-obvious behaviour, inline, by source path +
  version. See `ground-truth-verification.md`.
- **Detail pass.** After drafting, re-read each criterion and split compounds, tighten
  vague verbs, and confirm each is independently testable.

## Annotated example (abridged)

```markdown
### Requirement 3: Current Version Selection

**User Story:** As a deployment operator, I want to set or unset the Current Version of
a deployment, so that new and auto-upgrade traffic routes to the intended build.

#### Acceptance Criteria
1. WHEN SetCurrentVersion is called with a build_id naming an existing version, THE
   runtime SHALL set that version as current, update current_version_changed_time, and
   increment revision_number.
2. WHEN SetCurrentVersion is called with an empty build_id, THE runtime SHALL set the
   current version to nil, routing affected traffic to unversioned workers.
3. WHEN the version being set as current is the deployment's current ramping version,
   THE runtime SHALL automatically unset the ramping version in the same transition.
4. IF SetCurrentVersion supplies a non-nil conflict_token that does not match the
   deployment's current token, THEN THE Edge SHALL return FAILED_PRECONDITION and SHALL
   NOT mutate routing state.
8. IF the named build_id does not correspond to an existing version (with
   allow_no_pollers false), THEN THE Edge SHALL return NOT_FOUND.
```

Why this is good:
- Each criterion is a single EARS sentence with one `SHALL`.
- Side effects (criterion 3) and guards (criterion 4) are separate, numbered, testable.
- The error in criterion 8 names the exact code and was verified against the targeted
  source — not guessed.
