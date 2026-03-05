---
name: dagger-engine-dev
description: |
  Build, test, and develop the Dagger engine locally. Use when making engine changes,
  running integration tests, or needing to understand the local dev workflow.
  Keywords: hack/dev, engine, build, bin/dagger, integration test
---

# Dagger Engine Development

## Building the Engine

**Always use `./hack/dev` to build the engine. Never use `go build ./cmd/dagger/...` or any other method.**

**Always run it as a background process** (via `run_background`), then poll every 30 seconds with `list_background` until `running` is false. It can take several minutes — never use a short timeout.

```bash
./hack/dev
```

- Run from the repo root
- This builds the engine container image AND produces a `bin/dagger` CLI binary that points to it

## Why `./hack/dev` and Not `go build`

The Dagger engine is not a single binary. It consists of:

1. A **CLI** (`cmd/dagger/`) — the `dagger` command users run
2. An **engine container image** — where the actual work happens (built with Docker/Dagger itself)

`go build ./cmd/dagger/` only builds the CLI, which will still talk to the old engine image.
`./hack/dev` builds **both** and wires them together so your code changes are actually exercised.

## After Building

After `./hack/dev` completes, use `bin/dagger` to test your changes:

```bash
bin/dagger version          # confirm it points to the dev engine
bin/dagger develop          # test codegen changes
```

## Common Mistakes

- **Don't** run `go build ./cmd/dagger/` and expect engine changes to be reflected
- **Don't** use a short timeout when running `./hack/dev` in the background — it takes multiple minutes
- **Don't** test against the released `dagger` binary when you've made engine changes; always use `bin/dagger`
- **Do** wait for `./hack/dev` to fully complete before testing

## Related Skills

- `dagger-codegen` — editing templates, `dagger develop`, `dagger client install`
- `dagger-chores` — regenerating generated files, bumping Go version