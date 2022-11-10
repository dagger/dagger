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

This will kick off the workflow in `.github./workflows/publish-engine.yml` that builds+pushes the engine image to our registry with a tag matching `ENGINE_VERSION`.

## Go SDK

### Prep

```console
git pull
git status  # make sure everything is clean
```

### Bumping an Engine dependency

The following is needed if the SDK requires a new version of the engine.

```console
cd ./sdk/go
git checkout -b go-sdk-update
```

1. Obtain the full image ref, including SHA, from our GHCR repo for the version of the engine the SDK should depend on.

- Our GHCR repo can be found [here](https://github.com/orgs/dagger/packages/container/package/engine).
- Locate the tag for the engine the SDK should depend on and note the digest of it.
  - The digest will be prefixed with `sha256:` and look like `a10730b1d305644b87c21630d27d9e15d424014ee7ac9bd44f4504fcfe92a8ca`

1. Update the value of `engineImageRef` in `client.go` to be `ghcr.io/dagger/engine:<version tag>@sha256:<digest>` ([line currently here](https://github.com/dagger/dagger/blob/349f8a1661f1a8422a8bef919a345bd6dd3f3d85/sdk/go/client.go#L19))

```console
git commit client.go -m 'sdk: go: bump engine dependency'
gh pr create
```

1. Ensure all tests in CI pass.
1. Additionally, manually test provisioning by running the following commands on both Linux and MacOS hosts directly

```console
export SHORT_DIGEST=a10730b1d305644b # replace the value with the first 16 chars of the digest of the engine image used above
docker rm -fv dagger-engine-$SHORT_DIGEST # ensure there's no existing image so we use the full provisioning code paths
rm -rf ~/.cache/dagger/* # ensure there's no existing local engine-session binaries for same reason as above
cd ../.. # go to the root of the dagger repo
go test -v -count=1 $(pwd)/core/integration # run the engine tests on your host, which exercises the provisioning code paths of the Go SDK
```

### Release

```console
export SDK_VERSION=v0.1.0
git tag sdk/go/${SDK_VERSION}
git push origin sdk/go/${SDK_VERSION}
```

## Python SDK

Ensure that all checks on the `main` branch are green. Pay special attention
to `lint / sdk / python` & `test / sdk / python` checks. Since these checks
only run when specific paths change (e.g. `sdk/python/**`), ensure that they
have passed for the last commit that changed this path. If not, you may end up
releasing a broken version.

### Bumping Engine dependency

The following is needed if the SDK requires a new version of the engine.

```console
cd ./sdk/python
git checkout -b python-sdk-update
```

1. Obtain the full image ref, including SHA, from our GHCR repo for the version of the engine the SDK should depend on.

- Our GHCR repo can be found [here](https://github.com/orgs/dagger/packages/container/package/engine).
- Locate the tag for the engine the SDK should depend on and note the digest of it.
  - The digest will be prefixed with `sha256:` and look like `a10730b1d305644b87c21630d27d9e15d424014ee7ac9bd44f4504fcfe92a8ca`

1. Update the value of `ENGINE_IMAGE_REF` in `src/dagger/connectors/base.py` ([lines currently here](https://github.com/dagger/dagger/blob/349f8a1661f1a8422a8bef919a345bd6dd3f3d85/sdk/python/src/dagger/connectors/base.py#L19-L22)) to be

```python
"ghcr.io/dagger/engine:<version tag>@sha256:"
"<digest>"
```

```console
git commit src/dagger/connectors/base.py -m 'sdk: python: bump engine dependency'
gh pr create
```

1. Ensure all tests in CI pass.
1. Additionally, manually test provisioning by running the following commands on both Linux and MacOS hosts directly

```console
export SHORT_DIGEST=a10730b1d305644b # replace the value with the first 16 chars of the digest of the engine image used above
docker rm -fv dagger-engine-$SHORT_DIGEST # ensure there's no existing image so we use the full provisioning code paths
rm -rf ~/.cache/dagger/* # ensure there's no existing local engine-session binaries for same reason as above
poetry run poe test # to be run the `sdk/python` directory in our dagger repo
```

### Release

When the above is looking good, you are ready to release:

```console
export SDK_VERSION=v0.1.0
git tag --sign sdk/python/${SDK_VERSION}
git push origin sdk/python/${SDK_VERSION}
```

This will trigger the [`Publish Python SDK`
workflow](https://github.com/dagger/dagger/actions/workflows/publish-sdk-python.yml)
which publishes [dagger-io to PyPI](https://pypi.org/project/dagger-io).
