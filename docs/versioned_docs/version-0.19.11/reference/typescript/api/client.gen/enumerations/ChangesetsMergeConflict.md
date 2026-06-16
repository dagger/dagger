[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ChangesetsMergeConflict

# Enumeration: ChangesetsMergeConflict

Strategy to use when merging multiple changesets with git octopus merge.

## Enumeration Members

### Fail

> **Fail**: `"FAIL"`

Attempt the octopus merge and fail if git merge fails due to conflicts

***

### FailEarly

> **FailEarly**: `"FAIL_EARLY"`

Fail before attempting merge if file-level conflicts are detected between any changesets
