# Workspace & Artifacts Design

This design addresses limitations in Dagger's current configuration and extensibility model through a series of proposals.

## Parts

| Part | Focus | Status |
|------|-------|--------|
| [1. Module vs. Workspace](./proposals/01-module-vs-workspace.md) | Project layout, config files, dependency model | Draft |
| 2. Workspace API | Replace `+defaultPath` with explicit context access | Pending |
| 3. Checks API | Extensible checking, module composition | Pending |
| 4. Ship API | Publishing and deploying | Pending |
| 5. Multiple Environments | Config per environment (dev, prod, PR) | Pending |
| 6. Test Splitting | Parallel CI execution | Pending |

## Dependency Graph

```
[1. Module vs. Workspace]
         │
         ▼
[2. Workspace API]
         │
         ▼
[3. Checks API] ──────► [4. Ship API]
         │
         ▼
[5. Multiple Environments]
         │
         ▼
[6. Test Splitting]
```

## References

- [Proposal discussion](https://gist.github.com/shykes/a00bfb0ef399eeeff9d6c09465846a4a)
- POC branch: `toolchains-v2`
