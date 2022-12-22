# Releasing

This document describes how to release:

- 🚙 Engine & CLI `v0.3.x`
- 🐹 Go SDK
- 🐍 Python SDK
- ⬡ Node.js SDK

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

At the end of this workflow, a new PR will automatically be created to bump the
Engine version in the various SDKs.

👉 **Merge this PR** as soon as all checks pass.

## 🐹 Go SDK

> **Warning**
> If not already performed, do an Engine release to ensure the Engine the SDK
> depends on is up to date with the latest APIs supported in the SDK.

> **Warning**
> Ensure that all checks on the `main` branch are green, otherwise you may be
> releasing a 💥 broken release.

👉 Manually test provisioning by running the following commands on your host:

```console
# ensure there's no existing container so we use the full provisioning code paths
docker rm -fv $(docker ps --filter "name=^dagger-engine-*" -qa)
docker volume prune -f
docker system prune -f
# ensure there's no existing local engine-session binaries for same reason as above
rm -rf ~/.cache/dagger/*
go test -v -count=1 $(pwd)/core/integration # run the engine tests on your host, which exercises the provisioning code paths of the Go SDK
```

👉 If your host is macOS, repeat the above on Linux. If your host is Linux,
repeat the above on macOS.

When the above is looking good, you are ready to release:

### Release

```console
export SDK_VERSION=v0.<MINOR>.<PATCH>
git tag sdk/go/${SDK_VERSION}
git push origin sdk/go/${SDK_VERSION}
```

This will trigger the [`publish-sdk-go`
workflow](https://github.com/dagger/dagger/actions/workflows/publish-sdk-go.yml)
which publishes [dagger.io/dagger to pkg.go.dev](https://pkg.go.dev/dagger.io/dagger).

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

👉 Manually test provisioning by running the following commands on your host:

```console
# ensure there's no existing container so we use the full provisioning code paths
docker rm -fv $(docker ps --filter "name=^dagger-engine-*" -qa)
docker volume prune -f
docker system prune -f
# ensure there's no existing local engine-session binaries for same reason as above
rm -rf ~/.cache/dagger/*
poetry run poe test # to be run the `sdk/python` directory in our dagger repo
```

👉 If your host is macOS, repeat the above on Linux. If your host is Linux,
repeat the above on macOS.

When the above is looking good, you are ready to release:

### Release

```console
export SDK_VERSION=v0.<MINOR>.<PATCH>
git tag sdk/python/${SDK_VERSION}
git push origin sdk/python/${SDK_VERSION}
```

This will trigger the [`Publish Python SDK`
workflow](https://github.com/dagger/dagger/actions/workflows/publish-sdk-python.yml)
which publishes [dagger-io to PyPI](https://pypi.org/project/dagger-io).

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

👉 Manually test provisioning by running the following commands on your host:

```console
# ensure there's no existing container so we use the full provisioning code paths
docker rm -fv $(docker ps --filter "name=^dagger-engine-*" -qa)
docker volume prune -f
docker system prune -f
# ensure there's no existing local engine-session binaries for same reason as above
rm -rf ~/.cache/dagger/*
yarn test # to be run the `sdk/nodejs` directory in our dagger repo
```

👉 If your host is macOS, repeat the above on Linux. If your host is Linux,
repeat the above on macOS.

When the above is looking good, you are ready to release:

### Release

```console
export SDK_VERSION=v0.<MINOR>.<PATCH>
git tag sdk/nodejs/${SDK_VERSION}
git push origin sdk/nodejs/${SDK_VERSION}
```

This will trigger the [`Publish Node.js SDK`
workflow](https://github.com/dagger/dagger/actions/workflows/publish-sdk-nodejs.yml)
which publishes [@dagger.io/dagger to NPM.js](https://www.npmjs.com/package/@dagger.io/dagger).

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
