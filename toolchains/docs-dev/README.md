# docs-dev Netlify Deploy Notes

This toolchain is the legacy docs deployment path and is being migrated to
`modules/netlify`.

## Why This Matters

Current legacy deploy code (`toolchains/docs-dev/main.go`) does this:

- Sets `NETLIFY_SITE_ID`.
- Calls `netlify deploy --branch=main --message ... --json`.
- Returns the resulting `deploy_id`.

Current legacy publish code defaults to querying Netlify deploys with
`?branch=main` and publishing the latest result.

This coupling is fragile because Netlify CLI `--branch` on `deploy` is a
deprecated compatibility flag (treated like an alias-style value), not a
first-class branch deploy control.

If you remove `--branch=main` from legacy deploy **without** updating legacy
publish selection, publish can fail to find the deploy.

## Migration To `modules/netlify`

Use native site resolution and explicit deploy IDs.

### 1. Configure Site Per Path (Native)

Prefer Netlify-native per-site link state (`.netlify/state.json`) in each site
directory.

You can set it with the module helper:

```bash
dagger call -y -m ./modules/netlify \
  --token=env:NETLIFY_AUTH_TOKEN \
  link --path docs --site-id docs-dagger-io
```

Or with Netlify CLI directly in the site directory:

```bash
cd docs
netlify link --id docs-dagger-io --auth "$NETLIFY_AUTH_TOKEN"
```

### 2. Deploy With `modules/netlify`

`modules/netlify` deploy now:

- does not require `--site`,
- resolves site natively from `.netlify/state.json` or `NETLIFY_SITE_ID`,
- returns `deploy_id`.

Use `site` arg only as an explicit override.

### 3. Publish By Explicit `deploy_id`

Do not rely on branch-filtered lookup for publish.

Recommended:

- keep the `deploy_id` returned by deploy,
- pass that exact ID to publish/restore logic.

Transitional option while `DocsDev.Publish` still exists:

- pass `deployment` explicitly so it does not query `?branch=main`.

## Summary

In migration:

- remove dependency on `--branch=main`,
- keep site identity in native Netlify config per site path,
- publish using the explicit deploy ID produced by deploy.
