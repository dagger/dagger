# Future SDK Tests

author: shykes
created: 2026-05-15
status: planned
related: future/module-test-cleanup.md
source commit: 200e400d5a1463e78b1d52001394d77f743c290a

## Purpose

This is the handoff inventory for integration tests that should leave
`core/integration` and be re-created in SDK-as-module repos.

The tests listed here are allowed to be removed from core after this document is
committed. Their source can be recovered from the source commit above. When
porting them, preserve the behavior under test, not the old `dagger module *` or
`dagger develop` command syntax.

For Go, the first target repo is `github.com/shykes/dagger-go-sdk`. For other
SDKs, use the corresponding future SDK module repo.

## Recovery Process

1. Recover the original test source from the source commit:
   `git show 200e400d5:core/integration/<file>.go`.
2. Port the behavior to the target SDK module's public API.
3. Replace old CLI command expectations with SDK-module behavior. Do not carry
   forward command names, command flags, or exact CLI error text unless the new
   SDK module intentionally exposes the same UX.
4. Keep core-only behavior in core. Runtime, `ModuleSource`, workspace loading,
   schema, cache, services, and legacy compatibility tests belong in
   `core/integration` and should be converted to fixtures per
   `future/integration-test-fixtures.md`.

## Inventory

Rows use short commit `200e400d5`; the full source commit is in the header.

| Test | Recover from | Ultra-TLDR purpose | Ultra-TLDR SDK home |
|---|---|---|---|
| `CLISuite.TestModuleInit / name defaults to source root dir name` | `200e400d5:core/integration/module_init_cli_test.go:24` | inferred module name works | SDK init API; Go first |
| `CLISuite.TestModuleInit / source dir default` | `200e400d5:core/integration/module_init_cli_test.go:47` | default source layout per SDK | Go/Python/TS SDK modules |
| `CLISuite.TestModuleInit / source is made rel to source root by engine` | `200e400d5:core/integration/module_init_cli_test.go:85` | normalize init source path | SDK init API; Go first |
| `CLISuite.TestModuleInit / works inside subdir of other module` | `200e400d5:core/integration/module_init_cli_test.go:101` | nested init does not find-up | SDK init API; Go first |
| `CLISuite.TestModuleInit / init with absolute path` | `200e400d5:core/integration/module_init_cli_test.go:123` | absolute target path works | SDK init API; Go first |
| `CLISuite.TestModuleInit / init with absolute path and src .` | `200e400d5:core/integration/module_init_cli_test.go:141` | reject source escaping root | SDK init API; Go first |
| `CLISuite.TestModuleInitGit` | `200e400d5:core/integration/module_init_cli_test.go:155` | generated-file git metadata | Go/Python/TS SDK modules |
| `CLISuite.TestModuleDevelop / name and sdk` | `200e400d5:core/integration/module_develop_cli_test.go:22` | set SDK/source, reject rewrites | Go SDK generate/config |
| `CLISuite.TestModuleDevelop / source is made rel to source root by engine` | `200e400d5:core/integration/module_develop_cli_test.go:92` | normalize generate source path | Go SDK generate/config |
| `CLISuite.TestModuleDevelop / fails on git` | `200e400d5:core/integration/module_develop_cli_test.go:140` | generate requires local source | Go SDK generate/config |
| `CLISuite.TestModuleDevelop / source is made rel to source root by engine with absolute path` | `200e400d5:core/integration/module_develop_cli_test.go:153` | absolute source normalization | Go SDK generate/config |
| `CLISuite.TestModuleDevelop / recursive` | `200e400d5:core/integration/module_develop_cli_test.go:197` | regenerate parent and deps | SDK `generateAll` API |
| `CLISuite.TestModuleDevelopDeterministicCodegen / go split methods across files` | `200e400d5:core/integration/module_develop_cli_test.go:229` | Go codegen is repeatable | `dagger-go-sdk` |
| `CLISuite.TestModuleDevelopDeterministicCodegen / go with dependencies` | `200e400d5:core/integration/module_develop_cli_test.go:271` | Go dep codegen is repeatable | `dagger-go-sdk` |
| `GoSuite.TestInit` | `200e400d5:core/integration/module_go_test.go:29` | Go bootstrap/layout/run | `dagger-go-sdk` |
| `CLISuite.TestModuleDependencyInstall` | `200e400d5:core/integration/module_dependency_cli_test.go:27` | add deps, refs, pins, errors | SDK deps API |
| `CLISuite.TestModuleDependencyInstallOrder` | `200e400d5:core/integration/module_dependency_cli_test.go:460` | stable dependency ordering | SDK deps API |
| `CLISuite.TestModuleDependencyUninstall` | `200e400d5:core/integration/module_dependency_cli_test.go:516` | remove dependency entries | SDK deps API |
| `CLISuite.TestModuleDependencyUpdate` | `200e400d5:core/integration/module_dependency_cli_test.go:735` | update refs and pins | SDK deps API |
| `ModuleConfigSuite.TestDepWritePins` | `200e400d5:core/integration/module_config_test.go:1450` | install writes git pins | SDK deps API |
| `ModuleConfigSuite.TestDepPinsStayPinned` | `200e400d5:core/integration/module_config_test.go:1411` | generate preserves pins | SDK deps/generate API |
| `ModuleConfigSuite.TestConfigs / Allows $schema keyword` | `200e400d5:core/integration/module_config_test.go:181` | generate preserves `$schema` | SDK generate API |
| `ModuleConfigSuite.TestDaggerGitWithSources / develop half` | `200e400d5:core/integration/module_config_test.go:1316` | git source layouts after generate | SDK generate API |
| `ModuleSuite.TestModuleDevelopVersion` | `200e400d5:core/integration/module_engine_version_test.go:194` | generate updates engine version | SDK engine/version API |
| `WorkspaceModulesSuite.TestWorkspaceModuleInit` | `200e400d5:core/integration/workspace_modules_test.go:300` | create workspace-local modules | SDK init API |
| `WorkspaceModulesSuite.TestWorkspaceModuleMutation / local dependency updates are rejected when unsupported` | `200e400d5:core/integration/workspace_modules_test.go:273` | reject local dep update | SDK deps API |
| `WorkspaceModulesSuite.TestModuleScopedDependencyCommands` | `200e400d5:core/integration/workspace_modules_test.go:430` | module deps skip workspace config | SDK deps API |
| `ModuleSuite.TestNestedClientCreatedByModule` | `200e400d5:core/integration/module_nested_cli_test.go:18` | nested recursive generation | `dagger-go-sdk` generate-all |
| `ModuleSuite.TestDevelopRefreshesParentCodegenAfterLocalDependencyAPIChange` | `200e400d5:core/integration/module_dependency_runtime_test.go:261` | parent sees dep API change | Go/Python/TS SDK modules |
| `ModuleSuite.TestDevelopRefreshesLocalDependencyImplementationChanges` | `200e400d5:core/integration/module_dependency_runtime_test.go:329` | parent sees dep impl change | SDK modules with local deps |
| `PythonSuite.TestInit` | `200e400d5:core/integration/module_python_test.go:33` | Python bootstrap/layout/run | Future Python SDK module |
| `PythonSuite.TestPipLock / init-develop subtests` | `200e400d5:core/integration/module_python_test.go:953` | Python lockfile policy | Future Python SDK module |
| `TypescriptSuite.TestInit` | `200e400d5:core/integration/module_typescript_test.go:30` | TS bootstrap/layout/run | Future TypeScript SDK module |
| `TypescriptSuite.TestPackageManagerDetection` | `200e400d5:core/integration/module_typescript_test.go:900` | TS package manager choice | Future TypeScript SDK module |
| `TypescriptSuite.TestBundleLocalMigration` | `200e400d5:core/integration/module_typescript_test.go:2024` | TS SDK bundle migration | Future TypeScript SDK module |
| `JavaSuite.TestInit` | `200e400d5:core/integration/module_java_test.go:28` | Java SDK alias/ref init | Future Java SDK module |
| `PHPSuite.TestInit` | `200e400d5:core/integration/module_php_test.go:26` | PHP SDK alias/ref init | Future PHP SDK module |
| `ElixirSuite.TestInit` | `200e400d5:core/integration/module_elixir_test.go:27` | Elixir SDK alias/ref init | Future Elixir SDK module |
| `ClientGeneratorTest.TestPersistence / develop regeneration` | `200e400d5:core/integration/client_generator_test.go:576` | clients survive regeneration | Client generator module, if kept |

## Deletion Rule

After this file is committed, remove these core tests instead of preserving them
with fixture conversion. The recovery contract is the source commit above plus
this table.

If implementation finds that a row is also the only remaining regression test
for a core API, add replacement core coverage first, then remove the old
command-shaped test.
