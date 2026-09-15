# Module client SDK selection

created: 2026-09-08
status: implemented

Each add or remove command changes at most one SDK scope. The module target
remains required.

```text
dagger module client add <module> [--sdk=SDK]
dagger module client rm <module> [--sdk=SDK]
```

1. **CLI and help**

   Add optional `--sdk` to both commands. Remove SDK subcommands and SDK
   settings flags from `client add`. Show installed SDK names, sorted, in the
   `--sdk` help text. Show when none are installed. Read the selected workspace
   config, including remote workspaces. Preserve `module init` behavior.

   Update [module_sdk.go](../internal/cmd/dagger/module_sdk.go) and
   [module_sdk_dynamic.go](../internal/cmd/dagger/module_sdk_dynamic.go).
   Separate client help registration from module-init SDK settings inspection.

2. **Scope detection for add**

   With `--sdk`, inspect only that installed SDK. Without it, inspect every
   installed SDK in sorted name order. For each SDK, find its deepest registered
   scope in `dagger.toml` that contains the current directory. Call
   `findClientRoot()`. Validate the returned path with the existing rules.
   Select the deeper of the registered and detected paths.

   Reuse `resolveCurrentSDKModuleScope` in
   [workspace_sdk_module.go](../core/schema/workspace_sdk_module.go).
   Skip an SDK only when neither path exists. Abort if an inspected SDK fails.

3. **Scope selection for add**

   Select the unique deepest scope across the inspected SDKs. If several SDKs
   have that deepest scope, abort. Show their names and paths. Explain how to
   select one with `--sdk`. Abort clearly if no scope exists or the specified
   SDK is not installed.

   Complete detection and selection against the original workspace before
   changing any client record or starting generation.

4. **Add operation**

   Use the existing operation for one scope. Accept explicit local paths or
   module addresses. Reject installed names and unmarked local paths. Keep
   explicit path markers in saved local targets. Reject
   a duplicate only in the selected SDK's selected scope. Add the record,
   validate the configuration, and generate that scope. Apply the resulting
   workspace change through the existing CLI process.

5. **Remove operation**

   Match the exact stored target string. Search recorded clients whose scopes
   contain the current directory. If supplied, `--sdk` restricts this search.
   Select the deepest matching scope. Abort clearly on no match, an SDK that is
   not installed, or a tie. For a tie, show the SDK names and paths, with
   `--sdk` guidance. Remove only the client record and preserve the scope.
   Regenerate that scope. If invalid old targets remain, save the removal,
   report those targets, and skip generation until they are corrected or
   removed. Other SDK or runtime errors still fail the operation.

   Removal does not call `findClientRoot()`. Clients have no separate
   installation name. No alternative removal key is added.

6. **List output and removal**

   `list` shows every client that `rm` can find from the current directory.
   `list --all` shows every recorded client. An explicit `--sdk` filters either
   list. `TARGET` contains the complete stored removal key. `SDK` and `SCOPE`
   identify where the client is recorded. Scope paths are relative to the
   workspace root.

   To remove a specific row, run this from its listed scope:

   ```text
   dagger module client rm <TARGET> --sdk=<SDK>
   ```

7. **API and settings**

   Make `sdk` optional in `Workspace.withClient` and `Workspace.withoutClient`.
   Keep explicit API settings for `withClient`, but require an explicit SDK
   when settings are supplied. Preserve the existing use of saved settings.

   Update [workspace.go](../core/schema/workspace.go), its argument types, and
   the CLI queries. Preserve the existing v1 API version gates. Update API
   descriptions, generated bindings, command examples, and reference documents.

8. **Validation**

   Update tests for help output, argument handling, explicit SDK selection,
   both scope precedence cases, and selection across SDKs. Cover ties, missing
   scopes, SDK failures, duplicates, removal, and API settings. Verify removal
   from list output, including targets repeated across SDKs or scopes.

   Verify that failures leave client records unchanged. Each successful add or
   remove command changes exactly one scope's client records. Check that
   module-init settings still work and the API changes remain limited to v1.
