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

When published a PR will be opened automatically to bump the engine dependency in
the various SDKs. **Merge it** as soon as the tests pass.

## Go SDK

1. Ensure all tests in CI pass.
2. Additionally, manually test provisioning by running the following commands on both Linux and MacOS hosts directly

```console
# ensure there's no existing container so we use the full provisioning code paths
docker rm -fv $(docker ps --filter "name=^dagger-engine-*" -qa)
# ensure there's no existing local engine-session binaries for same reason as above
rm -rf ~/.cache/dagger/*
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

1. Ensure all tests in CI pass.
2. Additionally, manually test provisioning by running the following commands on both Linux and MacOS hosts directly

```console
# ensure there's no existing container so we use the full provisioning code paths
docker rm -fv $(docker ps --filter "name=^dagger-engine-*" -qa)
# ensure there's no existing local engine-session binaries for same reason as above
rm -rf ~/.cache/dagger/*
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
