# `@dagger.io/core`

The generated Dagger API bindings, `dag`, and the typed `connect`.

`client.gen.ts` is **generated** — 23k lines of it — by
`typescript-client:core-bindings`, which renders this engine's schema through
dagger/typescript-sdk's codegen. Do not edit it; `core-bindings/stale` fails
until it matches what that renders.

Everything else here is ordinary source belonging to this repo.

Its **version is the engine release**, not typescript-sdk's, which is why it is
published from here. `@dagger.io/session` is versioned independently, so
`package.json` depends on it by range rather than pinning it: a session release
should not need a core release to go with it.
