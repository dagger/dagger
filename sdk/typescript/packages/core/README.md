# `@dagger.io/core`

The generated Dagger API bindings, `dag`, and the typed `connect`.

**Every `.ts` file here comes from dagger/typescript-sdk** (`library/src/core/`)
and is produced by its `packager` module against a running engine. Do not edit
them here: a change that is not made upstream is silently dropped the next time
they are regenerated, and `client.gen.ts` alone is 23k lines. Their doc comments
are written from upstream's point of view for the same reason — `index.ts`
saying core is "published from dagger/dagger rather than from here" is correct
where it is maintained, and rewording it here would be dropped.

`package.json` and this file are not vendored; they belong to this repo.

Its **version is the engine release**, not typescript-sdk's, which is why it is
published from here. `@dagger.io/session` is versioned independently, so
`package.json` depends on it by range rather than pinning it: a session release
should not need a core release to go with it.
