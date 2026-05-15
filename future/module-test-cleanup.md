# Future Module Test Cleanup

author: shykes
created: 2026-05-14
updated: 2026-05-15
status: partially completed

## Context

The Dagger CLI is dropping commands that manage an individual Dagger module.
The commands in scope are:

- `dagger module init`
- `dagger develop`
- `dagger module install`
- `dagger module update`
- root module-dependency alias `dagger uninstall`
- hidden `dagger init`
- hidden `dagger publish`
- hidden `dagger client *` commands only if the final command-removal scope
  includes them

Workspace-level commands are not the target of this cleanup. Tests for
workspace config, workspace install/update, workspace lockfiles, runtime
behavior, schema behavior, cache behavior, services, and legacy compatibility
should stay in core unless their only subject is a removed module command.

This document is a handoff for the remaining test cleanup after the module
management commands leave the CLI.

## Already Completed

Completed in this branch:

- removed the CLI command implementations and registrations for `dagger module`,
  `dagger module init`, `dagger module install`, `dagger module update`,
  `dagger develop`, root module-dependency alias `dagger uninstall`, hidden
  `dagger init`, and hidden `dagger publish`
- kept workspace-level `dagger install`, `dagger update`, and `dagger lock
  update`
- removed the command-only helper code that only existed for deleted module
  management commands
- added replacement core coverage before deleting command-shaped assertions:
  - `ModuleConfigSuite.TestOldModuleConfig`
  - `ModuleConfigSuite.TestOldModuleConfigPinnedDeps`
  - `ModuleSuite.TestCustomSDKRuntime`
  - `ModuleSuite.TestPartialCustomSDKRuntime`
  - `ModuleLoadingSuite.TestModuleSourceAddressValidation`
- removed the completed command-surface test rows marked **done** below

Still remaining:

- move or waive SDK-owned authoring coverage listed under **Move Or Adapt Out Of
  Core**
- delete remaining command-surface integration tests that still invoke removed
  commands
- convert core tests that still use removed commands as setup to checked-in
  fixtures, per `future/integration-test-fixtures.md`
- run a main-vs-workspace command surface review before adding command
  presence/absence guard tests, per
  `future/workspace-command-migration-review.md`

## Decision Rules

Use exactly one disposition for each behavior:

- **Delete from core** when the test only covers the old CLI command surface:
  command routing, flags, help grouping, workspace-policy errors, deprecated
  flags, span naming, or command-specific UX.
- **Move/adapt out of core** when the test covers module-authoring behavior that
  should be owned by an SDK-as-module repo. Do not preserve old CLI syntax or
  exact CLI error messages when moving; preserve the underlying behavior.
- **Keep/convert in core** when the test covers engine, workspace, runtime,
  schema, cache, service, or legacy behavior and only uses dynamic module
  creation as setup. Convert those setup paths to fixtures.

There is no final "split" bucket. Disposition is per behavior row, not always
per Go test function. A parent test function may need mixed mechanical edits
if it contains several behaviors, but each behavior should resolve to delete,
move/adapt, or keep/convert.

Coverage guardrail: do not delete a parent test just because it invokes a
removed command. If the body is also the only regression test for an engine API,
SDK provider lifecycle, config compatibility rule, module loading rule, or
dependency behavior, first add replacement coverage in core or in the relevant
SDK-as-module repo. Only then remove the old command-shaped test.

## Recommended Order

1. Move or consciously waive the coverage listed under **Move Or Adapt Out Of
   Core**. Go-specific authoring coverage should move first because
   `github.com/shykes/dagger-go-sdk` already exists.
2. Delete the remaining pure CLI-surface rows listed under **Delete From Core**.
   Avoid deleting whole parent tests when they contain mixed behavior.
3. Convert remaining core integration setup helpers to use checked-in fixtures
   instead of dynamically creating modules.
4. Remove or shrink any dynamic setup helpers once no tests depend on them.

## Delete From Core

These rows test the old CLI command surface itself. Delete them from core when
the corresponding commands are removed.

| Status | Test name | Test file | Ultra-TLDR | Why delete? |
|---|---|---|---|---|
| done | `CLISuite.TestModuleInitLicense` | `core/integration/module_init_cli_test.go:155` | init/develop `--license` behavior | Deprecated CLI flags removed |
| done | `ModuleConfigSuite.TestConfigs / command-path assertions` | `core/integration/module_config_test.go:55` | develop/install validation dupes | Removed command paths |
| done | `ModuleConfigSuite.TestIncludeExclude / after develop` | `core/integration/module_config_test.go:913` | duplicate after-develop check | Develop-only assertion |
| todo | `ClientGeneratorTest.TestClientCommands` | `core/integration/client_generator_test.go:1472` | hidden `client list/uninstall` | Delete if client cmds removed |
| todo | `ClientGeneratorTest.TestClientUpdate` | `core/integration/client_generator_test.go:1566` | hidden `client update` | Delete if client cmds removed |
| done | `TestInitCommandRouting` | `cmd/dagger/workspace_test.go:16` | init/module-init routing | Commands removed |
| done | `TestInstallAndUpdateCommandFlags / module subcases` | `cmd/dagger/workspace_test.go:26` | module install/update flags | Commands removed |
| done | `TestWorkspaceCommandGrouping / hidden init` | `cmd/dagger/workspace_test.go:51` | hidden init grouping | Init removed |
| done | `TestRootHelpShowsWorkspaceCommandGroup / module delimiter` | `cmd/dagger/workspace_test.go:78` | module group expectation | Module group no longer needed as delimiter |
| done | `TestGenHelpDoesNotPanicWithModuleSubcommands` | `cmd/dagger/workspace_test.go:138` | gen help with module cmds | Module cmds gone |
| done | `TestSpanName / module update row` | `cmd/dagger/span_name_test.go:20` | span name for removed cmd | Command removed |
| done | `TestWorkspaceSelectionCommandPolicy / module-centric commands reject -W` | `core/integration/workspace_selection_test.go:354` | `dagger develop -W` policy | Develop removed |
| done | `TestOriginToPath` | `cmd/dagger/module_test.go:10` | publish helper path conversion | Publish command removed |

## Move Or Adapt Out Of Core

These rows test module-authoring behavior. Move the behavior to SDK-as-module
repos instead of converting these tests to core fixtures. Remove the core tests
after equivalent coverage is moved or after deciding the coverage is no longer
needed.

For Go, the target repo is `github.com/shykes/dagger-go-sdk`. For other SDKs,
the target is the corresponding future SDK module repo.

| Test name | Test file | Ultra-TLDR | Move/adapt target |
|---|---|---|---|
| `CLISuite.TestModuleInit / SDK bootstrap and layout` | `core/integration/module_init_cli_test.go:23` | init-created SDK file layout | SDK module init coverage |
| `CLISuite.TestModuleInitGit` | `core/integration/module_init_cli_test.go:223` | gitignore/gitattributes for generated SDK files | SDK module bootstrap coverage |
| `CLISuite.TestModuleDevelop / SDK/source authoring` | `core/integration/module_develop_cli_test.go:21` | set/update SDK and source path | SDK module config/generate coverage |
| `CLISuite.TestModuleDevelop / recursive generation` | `core/integration/module_develop_cli_test.go:180` | regenerate parent and local deps | SDK module `generateAll` coverage |
| `GoSuite.TestInit` | `core/integration/module_go_test.go:29` | Go init/generate/bootstrap behavior | `dagger-go-sdk` |
| `CLISuite.TestModuleDevelopDeterministicCodegen` | `core/integration/module_develop_cli_test.go:225` | deterministic Go codegen | `dagger-go-sdk` generate determinism |
| `CLISuite.TestModuleDependencyInstall` | `core/integration/module_dependency_cli_test.go:27` | dep add refs/pins/errors | SDK-module deps API |
| `CLISuite.TestModuleDependencyInstallOrder` | `core/integration/module_dependency_cli_test.go:460` | dependency ordering | SDK-module deps API |
| `CLISuite.TestModuleDependencyUninstall` | `core/integration/module_dependency_cli_test.go:516` | dependency removal | SDK-module deps API |
| `CLISuite.TestModuleDependencyUpdate` | `core/integration/module_dependency_cli_test.go:735` | dependency update refs/pins | SDK-module deps API |
| `ModuleSuite.TestModuleDevelopVersion` | `core/integration/module_engine_version_test.go:194` | engine-version mutation | SDK-module engine API |
| `WorkspaceModulesSuite.TestWorkspaceModuleInit` | `core/integration/workspace_modules_test.go:300` | workspace module creation | SDK-module init API |
| `WorkspaceModulesSuite.TestModuleScopedDependencyCommands` | `core/integration/workspace_modules_test.go:430` | module deps do not mutate workspace config | SDK-module deps API |
| `WorkspaceModulesSuite.TestWorkspaceModuleMutation / local dependency updates are rejected when unsupported` | `core/integration/workspace_modules_test.go:273` | local dep update rejection | SDK-module deps API |
| `ModuleSuite.TestNestedClientCreatedByModule` | `core/integration/module_nested_cli_test.go:18` | nested recursive generation | `dagger-go-sdk` nested/generateAll coverage |
| `ModuleSuite.TestDevelopRefreshesParentCodegenAfterLocalDependencyAPIChange` | `core/integration/module_dependency_runtime_test.go:261` | local dep API refresh | `dagger-go-sdk` generate/deps coverage |
| `ModuleSuite.TestDevelopRefreshesLocalDependencyImplementationChanges` | `core/integration/module_dependency_runtime_test.go:329` | local dep impl refresh | `dagger-go-sdk` generate/deps coverage |
| `ModuleConfigSuite.TestDepPinsStayPinned` | `core/integration/module_config_test.go:1460` | pins stay pinned across generation | SDK-module deps/generate coverage |
| `ModuleConfigSuite.TestConfigs / Allows $schema keyword` | `core/integration/module_config_test.go:217` | generation preserves `$schema` | SDK-module generate coverage |
| `ModuleConfigSuite.TestDaggerGitWithSources / develop half` | `core/integration/module_config_test.go:1365` | git source layouts with generation | SDK-module generate coverage |
| `PythonSuite.TestInit` | `core/integration/module_python_test.go:33` | Python init/bootstrap behavior | Future Python SDK module |
| `PythonSuite.TestPipLock / init-develop subtests` | `core/integration/module_python_test.go:953` | Python lockfile behavior | Future Python SDK module |
| `TypescriptSuite.TestInit` | `core/integration/module_typescript_test.go:30` | TS init/bootstrap behavior | Future TypeScript SDK module |
| `TypescriptSuite.TestPackageManagerDetection` | `core/integration/module_typescript_test.go:900` | TS package manager detection | Future TypeScript SDK module |
| `TypescriptSuite.TestBundleLocalMigration` | `core/integration/module_typescript_test.go:2024` | TS SDK bundle migration | Future TypeScript SDK module |
| `JavaSuite.TestInit` | `core/integration/module_java_test.go:28` | Java SDK alias/ref init | Future Java SDK module |
| `PHPSuite.TestInit` | `core/integration/module_php_test.go:26` | PHP SDK alias/ref init | Future PHP SDK module |
| `ElixirSuite.TestInit` | `core/integration/module_elixir_test.go:27` | Elixir SDK alias/ref init | Future Elixir SDK module |
| `ClientGeneratorTest.TestPersistence / develop regeneration` | `core/integration/client_generator_test.go:576` | clients regenerated by develop | Client generator replacement, if kept |

## Keep Or Convert In Core

Dynamic module creation in core integration tests should be removed, but not by
deleting tests whose behavior still belongs in core.

Keep these categories in core and convert setup to checked-in fixtures:

- engine and `ModuleSource` semantics
- runtime behavior
- schema and SDK-neutral module API behavior
- workspace selection and workspace loading behavior
- cache and cross-session behavior
- services, secrets, and networking behavior
- legacy compatibility behavior

Examples of tests that should generally stay in core after fixture conversion:

- module runtime behavior tests that use local dependencies as setup
- language signature/type tests outside the SDK init/bootstrap sections
- legacy compatibility tests where `module init` or `module install` only
  creates the test module
- cache/cross-session/service tests where module commands only prepare a module
- legacy module-config runtime compatibility from
  `module_config_compat_test.go`; delete only command rewrite side effects after
  equivalent runtime coverage exists
- custom SDK provider lifecycle and partial-provider behavior from
  `module_custom_sdk_test.go`; replace command setup, but do not drop coverage
  just because `module init` or `develop` triggered it
- source/path normalization behavior that is still exposed through
  `ModuleSource`; move command UX out, but keep direct API coverage in core if
  the engine API remains public

For these tests, replace setup helpers such as dynamic `module init`,
`develop`, and `module install` calls with reusable fixture directories under
`core/integration/testdata`.
