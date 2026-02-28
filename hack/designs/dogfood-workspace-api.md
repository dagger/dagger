# Dogfood: port toolchain modules to the Workspace API

Port all in-tree toolchain modules from deprecated `+defaultPath`/`+ignore` annotations to the Workspace API (`*dagger.Workspace`). Only toolchain modules are in scope — regular dependency modules use `+defaultPath` differently and are not affected.

See dagger/dagger#11812 and `skills/workspace-api-port/` for context and transformation patterns.

## Dependencies

- Docusaurus module details and optimization plan are tracked separately in:
  - [Module: Docusaurus (Workspace API Dogfood + Performance)](/Users/shykes/git/_workspace-api_2026-Feb17/github.com/shykes/dagger/hack/designs/module-docusaurus.md)

## Toolchains to port

- [ ] `toolchains/go` — 1 usage (Pattern B: root + ignore)
- [ ] `toolchains/engine-dev` — 2 usages in `main.go`, `bench.go` (Pattern B)
- [ ] `toolchains/cli-dev` — 2 usages in `main.go`, `publish.go` (Pattern B + E)
- [ ] `toolchains/docs-dev` — 1 usage in `main.go` (Pattern B). Not fully broken up yet: beyond docusaurus/markdownlint toolchains, project-specific docs logic remains to port/split.
- [x] `toolchains/helm-dev` — 1 usage (Pattern A: subdirectory)
- [ ] `toolchains/python-sdk-dev` — 2 usages (Pattern B + E)
- [ ] `toolchains/php-sdk-dev` — 3 usages (Pattern B + E)
- [ ] `toolchains/rust-sdk-dev` — 1 usage (Pattern B)
- [ ] `toolchains/installers` — 3 usages (Pattern C + E)
