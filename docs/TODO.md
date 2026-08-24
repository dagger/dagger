# Docs TODO

Deferred work that is intentionally not blocking the current restructure.

## Reference

The `/reference` top-level section was removed in the restructure. Each page below
still exists in git history under `docs/current_docs/reference/`; find it a home and
restore it, then re-link the callers listed with it and re-enable the matching
commented-out redirects in `netlify.toml`.

- **Workspace configuration** (`configuration/workspace.mdx`) — `dagger.toml` schema: top-level keys, `[modules.*]`, `[modules.*.settings]`, `entrypoint = true`, user-level config, module wiring, and precedence with `.env` / constructor defaults. The most-linked page of the set: `sdks/index.mdx`, `cli/environments.mdx`, and `cli/module-wiring.mdx` all pointed at it.
- **Module configuration** (`configuration/modules.mdx`) — `dagger-module.toml` schema and module-load filters.
- **Engine configuration** (`configuration/engine.mdx`) — engine settings, including cache pruning and privileged execution.
- **Cloud configuration** (`configuration/cloud.mdx`) — Dagger Cloud setup, Cloud Checks, tokens, and trace visibility.
- **Cache management** (`configuration/cache.mdx`) — cache configuration and pruning controls.
- **LLM integration** (`configuration/llm.mdx`) — model provider configuration.
- **Custom runner** (`configuration/custom-runner.mdx`) — the runner connection interface; linked from `adopting/scaling/index.mdx`.
- **Custom CA** (`configuration/custom-ca.mdx`) — certificate authority configuration.
- **Proxy configuration** (`configuration/proxy.mdx`) — network proxy settings.
- **Upgrade to Workspaces** (`upgrade-to-workspaces.mdx`) — migration guide from `dagger.json` modules; linked from the landing page and `adopting/workspace-setup.mdx`.

## Structure

- **Find a home for "How Dagger Works"** — the conceptual pages (workspaces, modules, functions, checks, cache, execution) were cut from the Module Developer Guide in the restructure; the content is in git history at `docs/current_docs/extending/how-dagger-works/`. Decide where the platform model belongs, restore it, and re-link the pages that referenced it (SDK guides, `getting-started/index.mdx`, `adopting/scaling/index.mdx`, `reference/configuration/cloud.mdx`) plus the commented-out `/extending/how-dagger-works` redirects in `netlify.toml`.
