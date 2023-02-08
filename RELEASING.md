# Releasing

This document describes how to release:

- 🚙 Engine & CLI `v0.3.x`
- 🐹 Go SDK
- 🐍 Python SDK
- ⬡ Node.js SDK
- Documentation

> **Warning**

> Ensure all SDKs have the same Engine version. If we publish one SDK with an
> updated Engine version, we **must** do the same for all other SDKs. This is
> important as currently our automatic provisioning code enforces the existence
> of a single Engine running at a time. Users will not be able to use multiple
> SDKs at the same time if the Engine version that they reference differs.

## 🚙 Engine & CLI

> **Warning**
> It's important to always do an Engine release prior to releasing any SDK.
> This will ensure that all the APIs in the SDK are also available in the
> Engine it depends on.

### Release

👉 Ensure that all checks are green in `main`

```console
export ENGINE_VERSION=v0.3.0
git checkout main
git pull
git status  # make sure everything is clean
git tag $ENGINE_VERSION
git push origin $ENGINE_VERSION
```

This will kick off the workflow in
[`.github./workflows/publish.yml`](https://github.com/dagger/dagger/actions/workflows/publish.yml)
that builds+pushes the engine image to our registry with a tag matching
`ENGINE_VERSION`. It also builds & publishes a new `dagger` CLI version.

At the end of this workflow, a new `draft` PR will automatically be created to bump the
Engine version in the various SDKs. Navigate towards the PR, and after marking
it as "ready to review", wait for all the checks to finish successfully.

👉 **Merge this PR** as soon as all checks pass.

### Post-release Checks

Verify the CLI publish worked as expected by [installing the CLI](https://docs.dagger.io/cli/465058/install) and verifying that `dagger version` outputs `$ENGINE_VERSION`

## 🐹 Go SDK

> **Warning**
> If not already performed, do an Engine release to ensure the Engine the SDK
> depends on is up to date with the latest APIs supported in the SDK.

> **Warning**
> Ensure that all checks on the `main` branch are green, otherwise you may be
> releasing a 💥 broken release.

### Release

First, ensure that you have the correct commit checked out locally. For most releases, the engine and SDKs are all being released at the same time, in which case you should have the commit that bumps engine SDK dependency versions checked out.

```console
export SDK_VERSION=v0.<MINOR>.<PATCH>
git tag sdk/go/${SDK_VERSION}
git push origin sdk/go/${SDK_VERSION}
```

This will trigger the [`publish-sdk-go`
workflow](https://github.com/dagger/dagger/actions/workflows/publish-sdk-go.yml)
which publishes [dagger.io/dagger to pkg.go.dev](https://pkg.go.dev/dagger.io/dagger).

### Post-release Checks

Verify everything is working as expected by running through the [Go SDK Get Started Guide](https://docs.dagger.io/sdk/go/959738/get-started).

- It takes a while for the go module servers to update the `latest` tag, so you can instead run `go get dagger.io/dagger@<SDK_VERSION`
- Running through all of the steps isn't necessary, just verifying the first step that executes a pipeline works is sufficient.

### Changelog

After the release is out, we need to create a release from the tag. Here is an
example of what we are aiming for 🚧 TBD. And here are the steps on how that
was created:

#### 1/5. Generate a draft release

To start the release notes, we need to have the [`gh`
CLI](https://cli.github.com/) installed, e.g. `brew install gh`. Once that is
installed, we can run our command:

```console
# To find the previously released SDK version, go to:
# https://github.com/dagger/dagger/releases?q=sdk%2Fgo&expanded=true
export PREVIOUS_SDK_VERSION=v0.<MINOR>.<PATCH>

gh release create sdk/go/${SDK_VERSION} --generate-notes --notes-start-tag sdk/go/${PREVIOUS_SDK_VERSION} --draft
```

#### 2/5. Clean up release notes

- Add this line to the top so SDK users can easily know which CLI+Runner version is compatible with the SDK:

```
This SDK is compatible with CLI+Runner version $ENGINE_VERSION
```

Replacing `$ENGINE_VERSION` with the value in `sdk/go/internal/engineconn/version.gen.go`.

- Add link to pkg.go.dev, e.g. `🐹 https://pkg.go.dev/dagger.io/dagger@v0.3.0`
- If there is a blog post (see **4/5.**) add a link to it, e.g.
  `📝 https://dagger.io/blog/go-sdk`
- If there is a video (see **4/5.**) add a link to it, e.g.
  `🎬 https://www.youtube.com/watch?v=0fSzOOZ2CO8`
- Click through each pull request and remove all the ones that don't change any
  Go SDK files. Some pull requests are prefixed with `sdk: go:`, which
  makes this process quicker.

> **Note**
> An approach that works is to open a dozen or so pull requests in new
> tabs, click on **Preview** and remove all the ones that don't affect this
> SDK. Repeat until all pull requests under **What's Changed** are relevant to
> this release.

- Remove all **New Contributors** which do not have a pull request
  under the **What's Changed** section.
- Lastly, remove **Full Changelog** since this will include changes across all
  SDKs + Engine + docs, etc.

#### 3/5. Publish release

- Validate release notes look good in `preview` mode
- ⚠️ De-select **Set as the latest release** (only used for Engine/CLI releases)
- Click on **Publish release**

#### 4/5. Update blog post

This is an optional step. We sometimes publish a blog post when a new SDK
release goes out. When that happens, we tend to include a link to the release
notes. Here is an example for the [Go SDK v0.3.0 release blog
post](https://dagger.io/blog/go-sdk).

You may also want to link to this blog post from within the release notes, e.g.
[sdk/go/v0.3.0](https://github.com/dagger/dagger/releases/tag/sdk%2Fgo%2Fv0.3.0).

If there is a video in this blog post, you may want to add it to the release
notes (see **3/5.**).

#### 5/5. Help promote the release

👉 DM release link to [@mircubed](https://github.com/mircubed).

## 🐍 Python SDK

> **Warning**
> If not already performed, do an Engine release to ensure the Engine the SDK
> depends on is up to date with the latest APIs supported in the SDK.

> **Warning**
> Ensure that all checks on the `main` branch are green, otherwise you may be
> releasing a 💥 broken release.

### Release

First, ensure that you have the correct commit checked out locally. For most releases, the engine and SDKs are all being released at the same time, in which case you should have the commit that bumps engine SDK dependency versions checked out.

```console
export SDK_VERSION=v0.<MINOR>.<PATCH>
git tag sdk/python/${SDK_VERSION}
git push origin sdk/python/${SDK_VERSION}
```

This will trigger the [`Publish Python SDK`
workflow](https://github.com/dagger/dagger/actions/workflows/publish-sdk-python.yml)
which publishes [dagger-io to PyPI](https://pypi.org/project/dagger-io).

### Post-release Checks

Verify everything is working as expected by running through the [Python SDK Get Started Guide](https://docs.dagger.io/sdk/python/628797/get-started).

- You may need to run `pip install --upgrade dagger.io` to force an update to the newest SDK version
- Running through all of the steps isn't necessary, just verifying the first step that executes a pipeline works is sufficient.

### Changelog

After the release is out, we need to create a release from the tag. Here is an
example of what we are aiming for
[sdk/python/v0.1.1](https://github.com/dagger/dagger/releases/tag/sdk%2Fpython%2Fv0.1.1).
And here are the steps on how that was created:

#### 1/5. Generate a draft release

To start the release notes, we need to have the [`gh`
CLI](https://cli.github.com/) installed, e.g. `brew install gh`. Once that is
installed, we can run our command:

```console
# To find the previously released SDK version, go to:
# https://github.com/dagger/dagger/releases?q=sdk%2Fpython&expanded=true
export PREVIOUS_SDK_VERSION=v0.<MINOR>.<PATCH>

gh release create sdk/python/${SDK_VERSION} --generate-notes --notes-start-tag sdk/python/${PREVIOUS_SDK_VERSION} --draft
```

#### 2/5. Clean up release notes

- Add this line to the top so SDK users can easily know which CLI+Runner version is compatible with the SDK:

```
This SDK is compatible with CLI+Runner version $ENGINE_VERSION
```

Replacing `$ENGINE_VERSION` with the value in `sdk/python/src/dagger/engine/_version.py`.

- Add link to PyPI, e.g. `🐍 https://pypi.org/project/dagger-io/0.1.1/`
- Add link to ReadTheDocs, e.g. `📖 https://dagger-io.readthedocs.io/en/sdk-python-v0.1.1/`
- If there is a blog post (see **4/5.**) add a link to it, e.g.
  `📝 https://dagger.io/blog/python-sdk`
- If there is a video (see **4/5.**) add a link to it, e.g.
  `🎬 https://www.youtube.com/watch?v=c0bLWmi2B-4`
- Click through each pull request and remove all the ones that don't change any
  Python SDK files. Some pull requests are prefixed with `sdk: python:`, which
  makes this process quicker.

> 💡 TIP: An approach that works is to open a dozen or so pull requests in new
> tabs, click on **Preview** and remove all the ones that don't affect this
> SDK. Repeat until all pull requests under **What's Changed** are relevant to
> this release.

- Remove all **New Contributors** which do not have a pull request
  under the **What's Changed** section.
- Lastly, remove **Full Changelog** since this will include changes across all
  SDKs + Engine + docs, etc.

#### 3/5. Publish release

- Validate release notes look good in `preview` mode
- ⚠️ De-select **Set as the latest release** (only used for Engine/CLI releases)
- Click on **Publish release**

#### 4/5. Update blog post

This is an optional step. We sometimes publish a blog post when a new SDK
release goes out. When that happens, we tend to include a link to the release
notes. Here is an example for the [Python SDK v0.1.1 release blog
post](https://dagger.io/blog/python-sdk).

You may also want to link to this blog post from within the release notes, e.g.
[sdk/python/v0.1.1](https://github.com/dagger/dagger/releases/tag/sdk%2Fpython%2Fv0.1.1).

If there is a video in this blog post, you may want to add it to the release
notes (see **3/5.**).

#### 5/5. Help promote the release

👉 DM release link to [@mircubed](https://github.com/mircubed).

## ⬡ Node.js SDK

> **Warning**
> If not already performed, do an Engine release to ensure the Engine the SDK
> depends on is up to date with the latest APIs supported in the SDK.

> **Warning**
> Ensure that all checks on the `main` branch are green, otherwise you may be
> releasing a 💥 broken release.

### Release

First, ensure that you have the correct commit checked out locally. For most releases, the engine and SDKs are all being released at the same time, in which case you should have the commit that bumps engine SDK dependency versions checked out.

```console
export SDK_VERSION=v0.<MINOR>.<PATCH>
git tag sdk/nodejs/${SDK_VERSION}
git push origin sdk/nodejs/${SDK_VERSION}
```

This will trigger the [`Publish Node.js SDK`
workflow](https://github.com/dagger/dagger/actions/workflows/publish-sdk-nodejs.yml)
which publishes [@dagger.io/dagger to NPM.js](https://www.npmjs.com/package/@dagger.io/dagger).

### Post-release Checks

Verify everything is working as expected by running through the [Node.js SDK Get Started Guide](https://docs.dagger.io/sdk/nodejs/783645/get-started).

- Running through all of the steps isn't necessary, just verifying the first step that executes a pipeline works is sufficient.

### Changelog

After the release is out, we need to create a release from the tag. Here is an
example of what we are aiming for
[sdk/nodejs/v0.2.0](https://github.com/dagger/dagger/releases/tag/sdk%2Fnodejs%2Fv0.2.0).
And here are the steps on how that was created:

#### 1/5. Generate a draft release

To start the release notes, we need to have the [`gh`
CLI](https://cli.github.com/) installed, e.g. `brew install gh`. Once that is
installed, we can run our command:

```console
# To find the previously released SDK version, go to:
# https://github.com/dagger/dagger/releases?q=sdk%2Fnodejs&expanded=true
export PREVIOUS_SDK_VERSION=v0.<MINOR>.<PATCH>

gh release create sdk/nodejs/${SDK_VERSION} --generate-notes --notes-start-tag sdk/nodejs/${PREVIOUS_SDK_VERSION} --draft
```

#### 2/5. Clean up release notes

- Add this line to the top so SDK users can easily know which CLI+Runner version is compatible with the SDK:

```
This SDK is compatible with CLI+Runner version $ENGINE_VERSION
```

Replacing `$ENGINE_VERSION` with the value in `sdk/nodejs/provisioning/default.ts`.

- Add link to NPMJS, e.g. `⬡ https://www.npmjs.com/package/@dagger.io/dagger`
- If there is a blog post (see **4/5.**) add a link to it, e.g.
  `📝 https://dagger.io/blog/nodejs-sdk`
- If there is a video (see **4/5.**) add a link to it, e.g.
  `🎬 https://www.youtube.com/watch?v=cuqmq_aTNfY`
- Click through each pull request and remove all the ones that don't change any
  Nodejs SDK files. Some pull requests are prefixed with `sdk: nodejs:`, which
  makes this process quicker.

> 💡 TIP: An approach that works is to open a dozen or so pull requests in new
> tabs, click on **Preview** and remove all the ones that don't affect this
> SDK. Repeat until all pull requests under **What's Changed** are relevant to
> this release.

- Remove all **New Contributors** which do not have a pull request
  under the **What's Changed** section.
- Lastly, remove **Full Changelog** since this will include changes across all
  SDKs + Engine + docs, etc.

#### 3/5. Publish release

- Validate release notes look good in `preview` mode
- ⚠️ De-select **Set as the latest release** (only used for Engine/CLI releases)
- Click on **Publish release**

#### 4/5. Update blog post

This is an optional step. We sometimes publish a blog post when a new SDK
release goes out. When that happens, we tend to include a link to the release
notes. Here is an example for the [Node.js SDK v0.1.0 release blog
post](https://dagger.io/blog/nodejs-sdk).

You may also want to link to this blog post from within the release notes, e.g.
[sdk/nodejs/v0.1.0](https://github.com/dagger/dagger/releases/tag/sdk%2Fnodejs%2Fv0.1.0).

If there is a video in this blog post, you may want to add it to the release
notes (see **3/5.**).

#### 5/5. Help promote the release

👉 DM release link to [@mircubed](https://github.com/mircubed).

## Documentation

> **Warning**
> Merging a documentation PR does NOT automatically deploy the
> new documentation to the production website.

There are two websites for documentation, corresponding to two stages
the release process:

- Staging website (https://devel.docs.dagger.io)
  - Netlify dashboard (https://app.netlify.com/sites/devel-docs-dagger-io)
- Production website (https://docs.dagger.io)
  - Netlify dashboard (https://app.netlify.com/sites/docs-dagger-io)

### Staging release

When a PR is merged, a new deployment is created for the documentation
site and it is automatically published to https://devel.docs.dagger.io
via Netlify.

Use this staging website to test the documentation, including:

- verifying that the new content appears in the navigation
- verifying internal and external links work correctly
- verifying that images appear correctly
- etc.

### Production release

When a PR is merged, a new production deployment is also created for
https://docs.dagger.io. However, this deployment is not automatically
published.

After testing the documentation using the staging website and if you
are satisfied with it, manually publish the
production deployment via Netlify as follows:

1. Log in to the [Netlify dashboard for https://docs.dagger.io](https://app.netlify.com/sites/docs-dagger-io).
2. Refer to the list of "production deploys" and select the one you wish
   to deploy. Usually, this will be the most recent one. You can confirm this
   by checking the deployment hash against the latest commit hash in the
   [dagger/dagger repository main branch](https://github.com/dagger/dagger).
3. On the deployment page, click the "Preview" button to once again
   preview/check the deployment. You can also check the deployment log to
   confirm there were no errors during the documentation build process.
4. If you are satisfied with the preview, click the "Publish deploy"
   button. This will publish the selected deployment on https://docs.dagger.io.

> 💡 TIP: There have been cases where Netlify builds have failed with errors,
> but the same build succeeds when performed locally. In the past, one reason
> for this has been Netlify's use of a stale cache. In case you encounter
> this error, click "Options -> Clear cache and retry with latest branch commit"
> to recreate the deployment with a clean cache.
