# SDK module UX demo

This module is a small branch of `github.com/dagger/go-sdk` for the module-max
prototype. It has the new `detectScope` and `generateScope` functions. It does
not use `currentModule.asSDK` and it does not declare `@generate` functions.

Module generation uses the engine's structured `ModuleManifest` helper and
adds its serialized file with `Workspace.withFile`. The SDK does not construct
TOML, select the default engine version, or infer the persisted module name.

The scope generator writes small Go client markers instead of production typed
clients. This keeps the demo focused on the engine and SDK-module contract.
