# Releasing

## Go SDK

### Prep

```console
git pull
git status  # make sure everything is clean
```

### Release Engine

```console
export ENGINE_VERSION=v0.3.0
git tag $ENGINE_VERSION
git push origin $ENGINE_VERSION
```

### Bump Engine dependency

```console
export ENGINE_VERSION=v0.3.0
cd ./sdk/go
go get github.com/dagger/dagger@${ENGINE_VERSION}
go mod tidy
git checkout -b go-sdk-update
git commit go.mod go.sum -m 'sdk: go: bump engine dependency'
gh pr create
```

### Release

```console
export SDK_VERSION=v0.1.0
git tag sdk/go/${SDK_VERSION}
git push origin sdk/go/${SDK_VERSION}
```

## Python SDK

Ensure that all checks on the `main` branch are green.  Pay special attention
to `lint / sdk / python` & `test / sdk / python` checks.  Since these checks
only run when specific paths change (e.g. `sdk/python/**`), ensure that they
have passed for the last commit that changed this path.  If not, you may end up
releasing a broken version.

When the above is looking good, you are ready to release:

```console
export SDK_VERSION=v0.1.0
git tag --sign sdk/python/${SDK_VERSION}
git push origin sdk/python/${SDK_VERSION}
```

This will trigger the [`Publish Python SDK`
workflow](https://github.com/dagger/dagger/actions/workflows/publish-sdk-python.yml)
which publishes [dagger-io to PyPI](https://pypi.org/project/dagger-io).
