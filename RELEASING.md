# Releasing

> **Warning**
> Currently, if any SDK bumps an engine dependency, we should bump the engine dependency of all other SDKs concurrently. This helps ensure that users running multiple SDKs from a single machine all use the same engine. This is important as currently our automatic provisioning code enforces the existence of a single engine running at a time.

## Engine

### Release

- Ensure that CI is green for all tests in main

```console
export ENGINE_VERSION=v0.3.0
git checkout main
git pull
git status  # make sure everything is clean
git tag $ENGINE_VERSION
git push origin $ENGINE_VERSION
```

This will kick off the workflow in [`.github./workflows/publish-engine.yml`](https://github.com/dagger/dagger/actions/workflows/publish-engine.yml) that builds+pushes the engine image to our registry with a tag matching `ENGINE_VERSION`.

When published a PR will be opened automatically to bump the engine dependency in
the various SDKs. **Merge it** as soon as the tests pass.

## Go SDK

Ensure that all checks on the `main` branch are green. Pay special attention
to `lint / sdk / go` & `test / sdk / go` checks. Ensure that these
have passed for the commit that you are about to tag. If not, you may end up
releasing a broken version.

Additionally, manually test provisioning by running the following commands on
both Linux and MacOS hosts directly

```console
# ensure there's no existing container so we use the full provisioning code paths
docker rm -fv $(docker ps --filter "name=^dagger-engine-*" -qa)
docker volume prune -f
docker system prune -f
# ensure there's no existing local engine-session binaries for same reason as above
rm -rf ~/.cache/dagger/*
go test -v -count=1 $(pwd)/core/integration # run the engine tests on your host, which exercises the provisioning code paths of the Go SDK
```

### Release

```console
export SDK_VERSION=v0.<MINOR>.<PATCH>
git tag sdk/go/${SDK_VERSION}
git push origin sdk/go/${SDK_VERSION}
```

## Python SDK

Ensure that all checks on the `main` branch are green. Pay special attention
to `lint / sdk / python` & `test / sdk / python` checks. Ensure that these
have passed for the commit that you are about to tag. If not, you may end up
releasing a broken version.

Additionally, manually test provisioning by running the following commands on
both Linux and MacOS hosts directly

```console
# ensure there's no existing container so we use the full provisioning code paths
docker rm -fv $(docker ps --filter "name=^dagger-engine-*" -qa)
docker volume prune -f
docker system prune -f
# ensure there's no existing local engine-session binaries for same reason as above
rm -rf ~/.cache/dagger/*
poetry run poe test # to be run the `sdk/python` directory in our dagger repo
```

### Release

When the above is looking good, you are ready to release:

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

#### 1/4. Generate a draft release

To start the release notes, we need to have the [`gh`
CLI](https://cli.github.com/) installed, e.g. `brew install gh`. Once that is
installed, we can run our command:

```console
# To find the previously released SDK version, go to:
# https://github.com/dagger/dagger/releases?q=sdk%2Fpython&expanded=true
export PREVIOUS_SDK_VERSION=v0.<MINOR>.<PATCH>

gh release create sdk/python/${SDK_VERSION} --generate-notes --notes-start-tag sdk/python/${PREVIOUS_SDK_VERSION} --draft
```

#### 2/4. Clean up release notes

- Add link to PyPI, e.g. 🐍 https://pypi.org/project/dagger-io/0.1.1/
- If there is a blog post (see **4/4.**) add a link to it, e.g.
  📝 https://dagger.io/blog/python-sdk
- If there is a video (see **4/4.**) add a link to it, e.g.
  🎬 https://www.youtube.com/watch?v=c0bLWmi2B-4
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

#### 3/4. Publish release

- ⚠️ De-select **Set as the latest release** (only used for Engine/CLI releases)
- Click on **Publish release**

#### 4/4. Update blog post

This is an optional step. We sometimes publish a blog post when a new SDK
release goes out. When that happens, we tend to include a link to the release
notes. Here is an example for the [Python SDK v0.1.1 release blog
post](https://dagger.io/blog/python-sdk).

You may also want to link to this blog post from within the release notes, e.g.
[sdk/python/v0.1.1](https://github.com/dagger/dagger/releases/tag/sdk%2Fpython%2Fv0.1.1).

If there is a video in this blog post, you may want to add it to the release
notes (see **3/4.**).

## Node.js SDK

Ensure that all checks on the `main` branch are green. Pay special attention
to `lint / sdk / nodejs` & `test / sdk / nodejs` checks. Ensure that these
have passed for the commit that you are about to tag. If not, you may end up
releasing a broken version.

Additionally, manually test provisioning by running the following commands on
both Linux and MacOS hosts directly

```console
# ensure there's no existing container so we use the full provisioning code paths
docker rm -fv $(docker ps --filter "name=^dagger-engine-*" -qa)
docker volume prune -f
docker system prune -f
# ensure there's no existing local engine-session binaries for same reason as above
rm -rf ~/.cache/dagger/*
yarn test # to be run the `sdk/nodejs` directory in our dagger repo
```

### Release

When the above is looking good, you are ready to release:

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

#### 1/4. Generate a draft release

To start the release notes, we need to have the [`gh`
CLI](https://cli.github.com/) installed, e.g. `brew install gh`. Once that is
installed, we can run our command:

```console
# To find the previously released SDK version, go to:
# https://github.com/dagger/dagger/releases?q=sdk%2Fnodejs&expanded=true
export PREVIOUS_SDK_VERSION=v0.<MINOR>.<PATCH>

gh release create sdk/nodejs/${SDK_VERSION} --generate-notes --notes-start-tag sdk/nodejs/${PREVIOUS_SDK_VERSION} --draft
```

#### 2/4. Clean up release notes

- Add link to NPMJS, e.g. ⬡ https://www.npmjs.com/package/@dagger.io/dagger
- If there is a blog post (see **4/4.**) add a link to it, e.g.
  📝 https://dagger.io/blog/nodejs-sdk
- If there is a video (see **4/4.**) add a link to it, e.g.
  🎬 https://www.youtube.com/watch?v=cuqmq_aTNfY
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

#### 3/4. Publish release

- ⚠️ De-select **Set as the latest release** (only used for Engine/CLI releases)
- Click on **Publish release**

#### 4/4. Update blog post

This is an optional step. We sometimes publish a blog post when a new SDK
release goes out. When that happens, we tend to include a link to the release
notes. Here is an example for the [Node.js SDK v0.1.0 release blog
post](https://dagger.io/blog/nodejs-sdk).

You may also want to link to this blog post from within the release notes, e.g.
[sdk/nodejs/v0.1.0](https://github.com/dagger/dagger/releases/tag/sdk%2Fnodejs%2Fv0.1.0).

If there is a video in this blog post, you may want to add it to the release
notes (see **3/4.**).
