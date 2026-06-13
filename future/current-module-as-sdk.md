# CurrentModule.asSDK

Status: proposed.

## Problem

`dagger generate` should stay generic: it discovers generators and runs them.
SDKs should not require CLI-side special cases.

But SDK generators need to know which modules and clients they manage in the
current workspace. That information now lives in `dagger.toml` under the SDK
module's `as-sdk` section:

```toml
[modules.go]
source = "github.com/dagger/go-sdk"

[[modules.go.as-sdk.modules]]
path = ".dagger"

[[modules.go.as-sdk.clients]]
path = "sdk"
module = "."
pin = "abc123"
```

SDKs should not scan the workspace or parse `dagger.toml` themselves.

## Proposed API

Add `asSDK` to `CurrentModule`:

```graphql
extend type CurrentModule {
  asSDK: CurrentModuleAsSDK!
}

type CurrentModuleAsSDK {
  name: String!
  modules: [CurrentModuleAsSDKModule!]!
  clients: [CurrentModuleAsSDKClient!]!
}

type CurrentModuleAsSDKModule {
  path: String!
  source: ModuleSource!
}

type CurrentModuleAsSDKClient {
  path: String!
  module: String!
  pin: String
  moduleSource: ModuleSource!
}
```

Expected SDK usage:

```go
sdk := dag.CurrentModule().AsSDK()
mods, err := sdk.Modules(ctx)
clients, err := sdk.Clients(ctx)
```

## Behavior

`dag.CurrentModule().AsSDK()` means: treat the currently executing module as an
SDK installed in the active workspace.

It resolves to the matching `[modules.<name>]` entry with an `as-sdk` marker,
then exposes that entry's persisted SDK role data:

- `modules` from `[[modules.<name>.as-sdk.modules]]`
- `clients` from `[[modules.<name>.as-sdk.clients]]`

If the current module is not installed as an SDK, error:

```text
current module is not installed as an SDK in this workspace
```

If multiple installed SDK entries could match, error instead of guessing.

## Field Rules

Expose fields that are part of the persisted `as-sdk` contract:

- module: `path`
- client: `path`, `module`, `pin`

Also expose engine-resolved helpers:

- module: `source`
- client: `moduleSource`

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
dag.CurrentModule().AsSDK()
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
- Resolve `modules` from `entry.AsSDK.Modules`.
- Resolve `clients` from `entry.AsSDK.Clients`.
- Add derived `source` and `moduleSource`.
- Test installed SDK, non-SDK current module, empty lists, populated lists, and
  duplicate SDK source installs.
