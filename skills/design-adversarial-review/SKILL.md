---
name: design-adversarial-review
description: Adversarial review of design docs, RFCs, and technical proposals for implementation readiness, cross-doc coherence, sequencing assumptions, user-facing clarity, and missing examples. Use when asked to critique, harden, or challenge a design before implementation, especially when ambiguous boundaries, hidden invariants, rollout order, or UX justification may cause divergent implementations.
---

# Design Adversarial Review

Perform a hostile review of design docs as an implementation surface, not as prose.

## Use This Workflow

1. Review all docs that define the system together, not one file at a time.
   Include any top-level document that defines boundaries or implementation order.

2. Separate these concerns:
   - design semantics
   - rollout sequencing assumptions
   - implementation readiness
   - user-facing clarity

3. Look for bugs in the docs, not style issues:
   - contradictions
   - undefined terms
   - hidden invariants
   - overloaded APIs
   - normalization rules that are implied but not stated
   - places where two strong implementers could build different things
   - places where the docs do not clearly say what changes for users and why it is better

4. Focus especially on:
   - schema/API contracts
   - CLI lowering rules
   - compiler steps
   - runtime invariants
   - dedup, batching, ordering, and equality rules
   - cross-doc ownership and boundary leaks
   - examples and acceptance tests that are missing for dangerous edge cases

5. Only report points that plausibly matter for implementation correctness, review clarity, or user understanding.
   Ignore wording nits unless they change meaning.

## Prompt Template

When using a sub-agent, adapt this prompt rather than improvising:

```text
Perform an adversarial design review of these docs as one combined system:

- <doc 1>
- <doc 2>
- <doc N>

Goal:
Find every place where a strong implementer could still build the wrong thing,
or where two strong implementers could build different things from the same
docs. Also find places where the docs fail to clearly explain what changes for
users and why this design is better.

Mindset:
Be hostile to ambiguity. Assume no tribal knowledge. Treat every undefined
term, hidden invariant, overloaded API, boundary leak, or hand-wave as a bug
until proven otherwise.

Important context:
- These docs define the full design, not PR-scoped subsets.
- The intended implementation order is: <sequence, if relevant>.
- Your review must check that this sequence is coherent:
  - each earlier component stands on its own terms
  - each later component builds cleanly on top
  - the boundary between them is sharp
- Do not review project management. Review the design and the sequencing
  assumptions only insofar as they affect coherence or correctness.

Review scope:
1. The docs as-designed, not speculative future features.
2. Cross-doc coherence.
3. Implementation readiness.
4. Edge cases.
5. User-facing clarity.

What to look for specifically:
- contradictions
- underspecified invariants
- hidden normalization rules
- APIs that require caller guesswork
- places where “compiler does X” is stated without enough rules to actually do X
- terms that could be interpreted more than one way
- schema shapes that imply a weaker or different model than the prose
- runtime validation doing work that should be encoded earlier, or at least
  stated explicitly
- batching / globbing / compatibility syntax that could produce multiple
  plausible compiled results
- places where the docs do not clearly state the user-visible before/after
  behavior or the reason for the design

Important:
- Do not nitpick wording unless it changes implementation or materially weakens
  understanding.
- Do not suggest aesthetic rewrites.
- Focus on bugs, ambiguity, implementation risk, and clarity gaps that would
  confuse reviewers or users.

Output format:
1. Blocking findings
   - short title
   - why it is a real implementation problem
   - exact file + line references
   - smallest doc change that would fix it
2. Non-blocking ambiguities
3. Missing examples / acceptance tests
4. User-facing clarity gaps
5. Final verdict: ready or not ready, with a short justification
```

## Review Heuristics

- If rollout order matters, make it explicit in the prompt.
- If one doc defines boundaries and another defines mechanics, review them together.
- If examples are doing real semantic work, test whether they constrain edge cases strongly enough.
- If the design introduces a compiler, require exact compile-time rules.
- If the design claims “smart compiler, dumb executor,” verify that runtime behavior is already determined by compiled objects.

## Response Shape

Default to:

1. Blocking findings
2. Non-blocking ambiguities
3. Missing examples / acceptance tests
4. User-facing clarity gaps
5. Final verdict

Keep summaries brief. Findings should dominate.
