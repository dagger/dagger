---
name: dagger-design-proposals
description: Write design proposals for Dagger features. Use when asked to draft, review, or iterate on Dagger design documents, RFCs, or proposals.
---

# Dagger Design Proposals

Guidelines for writing design proposals for Dagger features.

## Before Writing

**Always research first:**
1. Check existing skills (dagger-codegen, cache-expert, etc.) for relevant context
2. Look at related code in the Dagger codebase:
   - GraphQL schema: `core/schema/*.go`
   - CLI commands: `cmd/dagger/*.go`
   - Core types: `core/*.go`
3. Understand existing patterns before proposing new ones

## Structure

```markdown
# Part N: Title

*Builds on [Part N-1: Title](link)*

## Table of Contents
- [Problem](#problem)
- [Solution](#solution)
- [Core Concept](#core-concept)
- [CLI](#cli)
- [Status](#status)

## Problem

Numbered, concise limitations:

1. **Short title** - One sentence explanation.
2. **Short title** - One sentence explanation.

## Solution

One paragraph summary.

## Core Concept

GraphQL type definitions with inline docstrings:

```graphql
"""
Type description here.
"""
type Example {
  """Method description."""
  method(arg: String!): Result!
}
```

Go for implementation examples:

```go
func New(ws dagger.Workspace) *Example {
    // ...
}
```

## CLI

Real command examples:

```bash
$ dagger command --flag
OUTPUT
```

## Status

One line.

---

- Previous: [Part N-1](link)
- Next: [Part N+1](link) or "Part N+1: Title (coming soon)"
```

## Style

- **Concise** - Trust the reader. Remove fluff.
- **Tables** - Use for comparisons.
- **GraphQL for APIs** - Type definitions, not Go interfaces.
- **Go for implementation** - Examples showing how modules use the API.
- **Real examples** - go-toolchain, node-toolchain, not abstract Foo/Bar.
- **Less is more** - Remove sections when challenged.

## What to Avoid

- Separate "Methods" sections (use GraphQL docstrings)
- "Design Rationale" sections unless specifically valuable
- "Open Questions" that aren't real blockers
- Go for type definitions (use GraphQL)
- Layout examples that might confuse
- Over-explaining

## Process

1. **Gists as source of truth** - Publish early, iterate in gist
2. **Link parts together** - Previous/Next at bottom, "Builds on" at top
3. **Each part stands alone** - But builds on previous
4. **Iterate quickly** - User feedback drives changes

## Iterating with User

When you have clarifying questions or notes:
1. **List them first** - Present a high-level numbered list of all questions/notes
2. **One at a time** - Walk through each item individually, waiting for user response
3. **Don't dump** - Never present all questions with full details at once

## Codebase References

When writing proposals, reference actual Dagger code:

| Topic | Location |
|-------|----------|
| GraphQL schema definitions | `core/schema/*.go` |
| CLI commands | `cmd/dagger/*.go` |
| Core types (Directory, File, etc.) | `core/*.go` |
| Engine internals | `engine/*.go` |
| SDK codegen | `cmd/codegen/*.go` |

Example: To understand how `Host.findUp` works before proposing `Workspace.findUp`:
```bash
# Find the schema definition
grep -r "findUp" core/schema/host.go

# Find the implementation
grep -r "FindUp" core/host.go
```

## Publishing

```bash
# Create new gist
gh gist create file.md --desc "Dagger Design: Part N - Title" --public

# Update existing gist
gh gist edit GIST_ID file.md

# Post changelog comment (always do this after updates)
gh api --method POST /gists/GIST_ID/comments -f body="## Changelog
- Change 1
- Change 2"
```

**Always post a changelog comment** after updating a gist with significant changes.

## Related Skills

Check for other Dagger skills that may help with research:
- `dagger-codegen` - SDK codegen, templates, bindings
- `cache-expert` - Caching internals, invalidation
