# `@dagger.io/core`

The generated Dagger API bindings, `dag`, and the typed `connect`.

**Every `.ts` file here comes from dagger/typescript-sdk** (`library/src/core/`)
and is produced by its `packager` module against a running engine. Do not edit
them here: a change that is not made upstream is silently dropped the next time
they are regenerated, and `client.gen.ts` alone is 23k lines.

Its **version is the engine release**, not typescript-sdk's, which is why it is
published from this repo. `@dagger.io/session` is versioned independently and
pinned above.
