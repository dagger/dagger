# Netlify Module

`modules/netlify` deploys Netlify sites from Dagger and supports declarative
target routing in `netlify.toml`.

## Configuring Targets

Add custom `[[targets]]` blocks to your site's `netlify.toml`:

```toml
[[targets]]
git.branches = ["main"]
deploy.site = "devel-docs-dagger-io"
deploy.prod = true

[[targets]]
git.latestReleaseTag = true
deploy.site = "docs-dagger-io"
deploy.prod = true

[[targets]]
git.latestPreReleaseTag = true
deploy.site = "docs-dagger-io"
deploy.draft.alias = "next"
```

Supported keys:

- `git.branches = [String!]`: Branch glob patterns (for example `main`,
  `release/*`).
- `git.tags = [String!]`: Tag glob patterns (for example `v*`,
  `sdk-v*`). Also scopes `git.latest*Tag` resolution when set.
- `git.latestReleaseTag = Bool`: Match when the current tag is the latest
  stable semver tag in scope.
- `git.latestPreReleaseTag = Bool`: Match when the current tag is the latest
  prerelease semver tag in scope.
- `deploy.site = String!`: Netlify site name or site ID passed as `--site`.
- `deploy.prod = Bool`: Use `netlify deploy --prod`.
- `deploy.draft.alias = String`: Use `netlify deploy --alias <alias>` for a
  labeled draft deploy.

Validation rules:

- At least one `git.*` selector must be set in each target.
- `deploy.prod` and `deploy.draft.alias` are mutually exclusive.

Matching behavior:

- Selectors are ORed: branch match OR tag match OR latest release tag match OR
  latest prerelease tag match.
- Multiple targets can match in one run; each match triggers one deploy.

## Module Functions

On each discovered site:

- `targets(ws)` returns all configured targets as `[Target!]!`.
- `matchingTargets(ws)` returns only matched targets as `[Target!]!`.
- `targetGitContext(ws)` returns detected git branch/tags as JSON.
- `deployTargets(ws, build, message, context)` deploys once per matched target
  and returns deploy IDs.

On each target:

- `deploySite` returns the resolved Netlify site (`deploy.site`).
- `deployProd` returns whether the target deploys with `--prod`.
- `deployDraftAlias` returns the draft alias (`deploy.draft.alias`) when set.
- `deploy(ws, build, message, context)` deploys this one target and returns the
  deploy ID.
