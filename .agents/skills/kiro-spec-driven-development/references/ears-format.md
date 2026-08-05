# EARS — Easy Approach to Requirements Syntax

EARS constrains every acceptance criterion to a small set of keyword patterns so that
each one is unambiguous, atomic, and testable. Kiro acceptance criteria use the
uppercase keywords **WHEN, IF, WHILE, WHERE, THEN, THE, SHALL**.

## The keywords

| Keyword | Role |
|---|---|
| `WHEN`   | A trigger or event ("event-driven"). |
| `IF` … `THEN` | A precondition/guard and its consequence ("unwanted/optional behaviour"). |
| `WHILE`  | A continuous state during which the behaviour holds ("state-driven"). |
| `WHERE`  | A feature/configuration context in which the behaviour applies ("optional feature"). |
| `THE`    | Names the responsible component/actor (the subject of `SHALL`). |
| `SHALL`  | The obligation. Exactly one per criterion. |

## Canonical patterns

**Ubiquitous** (always true, no trigger):
```
THE {component} SHALL {required behaviour}.
```

**Event-driven** (something happens):
```
WHEN {trigger}, THE {component} SHALL {response}.
```

**State-driven** (during a state):
```
WHILE {state}, THE {component} SHALL {behaviour}.
```

**Optional-feature** (depends on a capability/config being present):
```
WHERE {feature/condition is present}, THE {component} SHALL {behaviour}.
```

**Unwanted/guarded behaviour** (precondition → consequence):
```
IF {condition}, THEN THE {component} SHALL {response}.
```

**Complex** (combine a context/state/trigger with a guard):
```
WHEN {trigger}, IF {condition}, THEN THE {component} SHALL {response}.
WHILE {state}, WHEN {trigger}, THE {component} SHALL {response}.
```

## Rules

1. **One `SHALL` per criterion.** If you wrote "and", you probably have two criteria —
   split them.
2. **Name the subject** with `THE {component}` so it is clear who is obligated.
3. **Be specific about outcomes**, especially errors. Prefer "THE Edge SHALL return
   `INVALID_ARGUMENT`" over "THE Edge SHALL reject the request."
4. **Make it testable.** A criterion you cannot write a check for is not yet a
   requirement — it is a wish. Rephrase until a pass/fail is obvious.
5. **Atomic and ordered.** Number criteria 1..N within each requirement so design and
   tasks can cite them precisely (e.g. `Requirements 2.3`).
6. **Resolve ambiguity against ground truth**, not assumption. If the exact error code,
   default, or ordering is unknown, verify it before writing the criterion.

## Worked rewrites

Prose → EARS:

- ✗ "The system should handle bad page sizes gracefully."
  ✓ `WHEN a list request supplies a page_size that is ≤ 0 or greater than the server
     maximum, THE Edge SHALL clamp page_size to the server maximum.`

- ✗ "Don't let people delete a deployment that still has versions."
  ✓ `IF a delete targets a deployment that still has one or more versions, THEN THE
     Edge SHALL return FAILED_PRECONDITION and SHALL NOT remove the record.`

- ✗ "Pinned workflows stay on their version."
  ✓ `WHILE a workflow's effective behaviour is PINNED, THE runtime SHALL route its
     tasks to the pinned version regardless of the deployment's current version.`

- ✗ "Support an override that wins over the normal behaviour."
  ✓ `WHERE a versioning_override is present on the execution, THE runtime SHALL apply it
     with precedence over the SDK-sent behaviour.`

## Compound-criterion splitting

A single sentence with two obligations becomes two numbered criteria:

- ✗ "WHEN create is retried with a seen request_id THE Edge SHALL no-op, and if the id
   is reused for a different key THE Edge SHALL return INVALID_ARGUMENT."
- ✓
  ```
  3. WHEN CreateX is retried with a request_id previously seen for the same key, THE
     Edge SHALL treat it as a successful no-op and return the existing token.
  4. IF the same request_id is reused for a different key, THEN THE Edge SHALL return
     INVALID_ARGUMENT.
  ```
