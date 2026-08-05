# Bugfix workflow — the bug-condition method

Bugfix specs use the requirements-first workflow but replace open-ended requirements with
a formalised **bug condition** and a fix expressed as properties. The goal is to prove
the bug exists, fix it, and prove the fix without regressing correct behaviour.

## When to use

Something that should work does not: a crash, an incorrect result, a regression, broken
error handling. Indicators: "fix / bug / crash / error / broken / fails / wrong /
doesn't work / regression / defect."

## Documents

A bugfix spec produces `bugfix.md` (in place of `requirements.md`), then `design.md`,
then `tasks.md`.

### bugfix.md

1. **Bug summary** — observed behaviour vs expected behaviour, in plain terms.
2. **Reproduction** — the minimal input/sequence that triggers it.
3. **Bug condition `C(X)`** — formalise the buggy state precisely: for input/state `X`,
   `C(X)` is true exactly when the bug manifests. This is the predicate the exploration
   test will assert.
4. **Root cause** — verified against ground truth (read the actual code path; do not
   guess). Cite the source location.
5. **Expected behaviour** — what the system should do for the same `X`, expressed as
   EARS acceptance criteria.
6. **Preservation set** — the behaviours that must NOT change (everything currently
   correct). The fix must preserve these.

### design.md

Express the fix as two kinds of properties:

- **Fix property:** *For any* `X` where the bug condition held, the corrected system
  exhibits the expected behaviour (`C(X)` is no longer satisfiable).
- **Preservation property:** *For any* input outside the bug condition, behaviour is
  unchanged from before the fix.

Both get `**Validates:**` lines tying them to the bugfix criteria.

### tasks.md — ordering is special

**Task 1 is always the bug-condition exploration property test**, written to assert that
`C(X)` does NOT occur — so it **fails on the unfixed code**. That failure is the success
signal: it confirms the bug exists and the test detects it.

- If the exploration test **fails as expected** → the bug is confirmed; record the
  shrunk counterexample and proceed to the fix tasks.
- If it **passes unexpectedly** → stop. Either the code already has a fix, the root
  cause is wrong, or the test logic is flawed. Re-investigate before writing fix tasks;
  do not proceed on a false premise.

Subsequent tasks: implement the fix (smallest change that removes `C(X)`), then add the
preservation property tests, then a checkpoint, then integration coverage.

## Discipline

- **Verify the root cause against ground truth** before writing the fix. A fix built on a
  wrong root cause wastes the whole chain.
- **Fix the cause, not the symptom.** If two attempts at the same approach fail,
  diagnose the root cause and try a fundamentally different approach rather than
  patching incrementally.
- **Preserve correct behaviour.** The preservation properties are what keep a fix from
  becoming a regression.
- **Smallest sufficient change.** A bug fix does not license refactoring surrounding
  code.
