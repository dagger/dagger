# CurrentModule.asSDK

Status: proposed.

## Problem

`dagger generate` should stay generic: it discovers generators and runs them.
SDKs should not require CLI-side special cases.

But SDK generators need to know which modules and clients they manage in the
current workspace. That information now lives in the top-level SDK registry in
`dagger.toml`:

```toml
[modules.go]
source = "github.com/dagger/go-sdk"

[sdks.go]
module = "go"

[sdks.go.claimed]
modules = [
  ".dagger",
]
clients = [
  { path = "sdk", module = "." },
]
```

SDKs should not scan the workspace or parse `dagger.toml` themselves.

## Proposed API

Add `asSDK` to `CurrentModule`:

```graphql
extend type CurrentModule {
  asSDK(workspace: Workspace!): CurrentModuleAsSDK!
}

type CurrentModuleAsSDK {
  name: String!
  modules: [CurrentModuleAsSDKModule!]!
  clients: [CurrentModuleAsSDKClient!]!
}

type CurrentModuleAsSDKModule {
  path: String!
}

type CurrentModuleAsSDKClient {
  path: String!
  module: String!
  moduleSource: ModuleSource!
}
```

Expected SDK usage:

```go
sdk := dag.CurrentModule().AsSDK(workspace)
mods, err := sdk.Modules(ctx)
clients, err := sdk.Clients(ctx)
```

## Behavior

`dag.CurrentModule().AsSDK(workspace)` means: treat the currently executing
module as an SDK installed in that workspace.

It resolves the `[sdks.<name>]` entry whose `module` field names the currently
executing installed module, then exposes its persisted claims:

- `modules` contains the selected paths from
  `sdks.<name>.claimed.modules`: every module at or below the workspace
  cwd, plus the nearest enclosing module when needed
- `clients` from `sdks.<name>.claimed.clients`

If the current module is not installed as an SDK, error:

```text
current module is not installed as an SDK in this workspace
```

If multiple installed SDK entries could match, error instead of guessing.

## Field Rules

Expose fields that are part of the persisted SDK claim contract:

- module: `path`
- client: `path`, `module`

Also expose the engine-resolved client helper:

- `CurrentModuleAsSDKClient.moduleSource`

Do not expose client `options` for now. The current config code has internal
round-trip support for arbitrary client fields, but this API should not make
that public or redesign it.

## Ownership

Engine owns:

- reading workspace config
- identifying the current module's SDK install entry
- resolving module/client refs to `ModuleSource`
- returning clear errors for missing or ambiguous SDK context

SDK owns:

- deciding what to generate
- interpreting its own language/toolchain files
- composing generation changes

## `dagger generate`

No special CLI behavior is needed.

`dagger generate` continues to run generators normally. An SDK generator that
needs its managed modules or clients calls:

```go
dag.CurrentModule().AsSDK(workspace)
```

## Implementation Note

Source-string matching is not enough. A workspace may install the same SDK
source more than once under different names or pins.

Prefer preserving the workspace install identity when the engine loads and runs
an installed SDK generator. If that identity is unavailable, fallback matching
is only valid when exactly one installed SDK entry matches.

## Handoff

- Add `CurrentModule.asSDK`.
- Add `CurrentModuleAsSDK`, `CurrentModuleAsSDKModule`, and
  `CurrentModuleAsSDKClient`.
- Resolve and filter modules from `entry.Claimed.Modules`.
- Resolve `clients` from `entry.Claimed.Clients`.
- Add the derived client `moduleSource` field.
- Test installed SDK, non-SDK current module, empty lists, populated lists, and
  duplicate SDK source installs.
