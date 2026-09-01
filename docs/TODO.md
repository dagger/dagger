# Docs TODO

Deferred work that is intentionally not blocking the current restructure.

## Tooling

- **File an issue: the docs tooling hardcodes page paths** — moving the API reference from `api/reference` to `reference/api` required editing four files outside `current_docs`, because the path is written into `plugins/dagger-api-reference/generate-stubs.js` (output directory and emitted slug), `src/components/api/data.ts`, `src/theme/DocItem/Layout/index.tsx`, and `sidebars.ts`. Moving a docs section must not need code changes.

  One constant is not enough. The React components are shared by every version, and each version keeps the prefix it was cut with, so a link has to be resolved against the active version. `data.ts` now exports `typeDocPrefixes` for this, and the components read it. The plugin and `sidebars.ts` still hardcode the path separately; they only ever write the current version, so they need the newest prefix, not the list. Make all four read from one place.

## Structure

- **Find a home for "How Dagger Works"** — the conceptual pages (workspaces, modules, functions, checks, cache, execution) were cut from the Module Developer Guide in the restructure; the content is in git history at `docs/current_docs/extending/how-dagger-works/`. Decide where the platform model belongs, restore it, and re-link the pages that referenced it (SDK guides, `getting-started/index.mdx`, `adopting/scaling/index.mdx`, `reference/configuration/cloud.mdx`) plus the commented-out `/extending/how-dagger-works` redirects in `netlify.toml`.
