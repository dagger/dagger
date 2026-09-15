---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Enumeration: ChangesetMergeConflict

Strategy to use when merging changesets with conflicting changes.

## Enumeration Members

### Fail

> **Fail**: `"FAIL"`

Attempt the merge and fail if git merge fails due to conflicts

***

### FailEarly

> **FailEarly**: `"FAIL_EARLY"`

Fail before attempting merge if file-level conflicts are detected

***

### LeaveConflictMarkers

> **LeaveConflictMarkers**: `"LEAVE_CONFLICT_MARKERS"`

Let git create conflict markers in files. For modify/delete conflicts, keeps the modified version. Fails on binary conflicts.

***

### PreferOurs

> **PreferOurs**: `"PREFER_OURS"`

The conflict is resolved by applying the version of the calling changeset

***

### PreferTheirs

> **PreferTheirs**: `"PREFER_THEIRS"`

The conflict is resolved by applying the version of the other changeset
