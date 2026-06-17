# Workspace Regression Tests

Run these with the engine-dev integration test helper:

```sh
dagger call engine-dev test --pkg ./core/integration --run <test> --failfast --timeout 8m --test-verbose
```

| Issue | Regression test | Command |
| --- | --- | --- |
| 1. High: `GitRef.asWorkspace` likely aliases its rootfs dependency to the returned `Workspace`. | `TestWorkspace/TestGitRefBackedSyntheticWorkspaceRoundTripsFromID` | `dagger call engine-dev test --pkg ./core/integration --run TestWorkspace/TestGitRefBackedSyntheticWorkspaceRoundTripsFromID --failfast --timeout 8m --test-verbose` |
| 2. High: overlay write APIs likely alias their `Changeset` dependency to the returned `Workspace`. Covers `withNewFile`, `withNewDirectory`, and `withChanges`. | `TestWorkspace/TestOverlayWorkspaceFunctionalWritesRoundTripFromID` | `dagger call engine-dev test --pkg ./core/integration --run TestWorkspace/TestOverlayWorkspaceFunctionalWritesRoundTripFromID --failfast --timeout 8m --test-verbose` |
| 3. High: chained overlays over `GitRef` lose earlier changes in `workspace.git().uncommitted`. | `TestWorkspace/TestChainedOverlayGitRefWorkspaceReportsAllOverlayChanges` | `dagger call engine-dev test --pkg ./core/integration --run TestWorkspace/TestChainedOverlayGitRefWorkspaceReportsAllOverlayChanges --failfast --timeout 8m --test-verbose` |
| 5. Medium: remote `-W` workspace rootfs includes `.git`, unlike `GitRef.asWorkspace`. | `TestWorkspaceSelection/TestDeclaredWorkspaceSelection/remote` | `dagger call engine-dev test --pkg ./core/integration --run TestWorkspaceSelection/TestDeclaredWorkspaceSelection/remote --failfast --timeout 8m --test-verbose` |
| 6. Medium: local-only mutation guards are uneven for remote workspaces. | `TestWorkspaceSelection/TestWorkspaceSelectionCommandPolicy/local-only` | `dagger call engine-dev test --pkg ./core/integration --run TestWorkspaceSelection/TestWorkspaceSelectionCommandPolicy/local-only --failfast --timeout 8m --test-verbose` |
| 7. Medium/low: overlay APIs are public on all `Workspace`, but host-local workspaces may fail unclearly. | `TestWorkspaceAPI/TestHostWorkspaceFunctionalOverlayAPIsRejectClearly` | `dagger call engine-dev test --pkg ./core/integration --run TestWorkspaceAPI/TestHostWorkspaceFunctionalOverlayAPIsRejectClearly --failfast --timeout 8m --test-verbose` |

Note: issue 3 can be masked by issue 1 or 2 until those are fixed, because the chained-overlay test starts from `GitRef.asWorkspace` and uses overlay writes.

Issue 4 is not listed yet. The first candidate test for dirty local `GitRepository.asWorkspace` passed on the current branch, so it was not a valid regression test. Add it only after finding a fixture that fails before the fix.
