# Docs TODO

Deferred work that is intentionally not blocking the current restructure.

## Structure

- **Find a home for "How Dagger Works"** — the conceptual pages (workspaces, modules, functions, checks, cache, execution) were cut from the Module Developer Guide in the restructure; the content is in git history at `docs/current_docs/extending/how-dagger-works/`. Decide where the platform model belongs, restore it, and re-link the pages that referenced it (SDK guides, `getting-started/index.mdx`, `adopting/scaling/index.mdx`, `reference/configuration/cloud.mdx`) plus the commented-out `/extending/how-dagger-works` redirects in `netlify.toml`.
