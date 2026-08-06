# sdk-sdk

Black-box contract checks for Dagger SDK modules — such as
`github.com/dagger/go-sdk`, `github.com/dagger/dang-sdk`,
`github.com/dagger/typescript-sdk`, and `github.com/dagger/python-sdk` — plus
tooling to start a new SDK helper module.

## Checking an SDK

Run the checks against an SDK repository:

```sh
dagger -m github.com/dagger/sdk-sdk -W <sdk-repo> check
```

The checks vendor the SDK module into a scratch git workspace inside a runner
container, install a release Dagger CLI, then drive the SDK through real CLI
commands the way a user would:

- `dagger sdk install ./<sdk>` registers the SDK and marks it `as-sdk` in
  `dagger.toml`.
- `dagger module init <sdk> test-mod` scaffolds a new module, writes its
  `dagger-module.toml`, installs it in `dagger.toml`, and records the SDK as
  the module's authoring SDK.
- `dagger generate` succeeds on the fresh scaffold.
- After generation the scaffolded module loads and serves its API:
  `dagger api functions test-mod` succeeds (starter templates may expose no
  functions yet).
- `dagger sdk module-options <sdk>` introspects the SDK's `initModule`
  capability.
- `dagger module engine required` and `dagger module deps list` work from the
  scaffolded module directory.
- `dagger generate` is anchored at the caller's cwd: run from inside one
  module's directory with a sibling module scaffolded elsewhere, the sibling
  is left untouched.

Function-level contract checks additionally call the SDK's `initModule`
directly (always with an explicit `--path`, as the engine does) and inspect
the returned changesets: `initModule` must seed at least one file, must not
write engine-owned config (`dagger.json` / `dagger-module.toml`), and must not
remove existing files. The SDK must also list a `@generate` hook in
`dagger generate -l`.

Each lifecycle stage and each contract behavior is its own check, so the check
report shows exactly what passed and what failed. All commands run through one
pipeline: when a stage fails, its own check captures the error, and the checks
that depend on it fail with a `prerequisite command failed` message naming that
stage instead of repeating raw errors.

Configure the CLI release with the top-level `dagger-cli-version` setting; the
default is `1.0.0-beta.9`. Individual targets accept `with-timeout` for slow
SDKs (the default command timeout is `10m`). Custom checks can reuse the
harness through `target`:

```dang
let testTarget = sdkSdk.target(module.workspaceView, module.sourceRootPath)
testTarget.install.assertSuccess
testTarget.runInModule(["module", "deps", "list"]).assertSuccess
```

## The SDK contract

Under CLI 1.0 the engine owns module bookkeeping — `dagger-module.toml`,
workspace config, and dependency and engine-version edits. An SDK module
implements only what is genuinely language-specific:

- `initModule(ws, name, path): Changeset!` — seed the SDK's own files for a new
  module. Because the engine owns module config, `initModule` must **not** write
  `dagger.json` / `dagger-module.toml`.
- a `@generate` hook — regenerate the modules the SDK manages. Managed modules
  are discovered via `currentModule.asSDK.modules`.

`initClient` (typed client generation) is an optional part of the contract and
is not required or exercised here.

Changeset paths are workspace-root-relative. For example, `initModule` for a
module named `my-sdk` is expected to seed files under `.dagger/modules/my-sdk/`.

## Starting a new SDK

sdk-sdk is itself an SDK for authoring Dang SDK helper modules:

```sh
dagger sdk install github.com/dagger/sdk-sdk
dagger module init sdk-sdk my-sdk
```

The module name is the Dagger module name. The generated Dang root type is
derived from it, for example `my-sdk` becomes `MySdk`.

Because sdk-sdk fulfills its own contract, running `dagger check` inside this
repository exercises every check against sdk-sdk itself.
