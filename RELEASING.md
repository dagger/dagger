# Releasing

## Prep

```console
git pull
git status  # make sure everything is clean
```

## Release Engine

```console
export ENGINE_VERSION=v0.3.0
git tag $ENGINE_VERSION
git push origin $ENGINE_VERSION
```

## Update Go SDK

```console
export ENGINE_VERSION=v0.3.0
cd ./sdk/go
go get github.com/dagger/dagger@${ENGINE_VERSION}
go mod tidy
git checkout -b go-sdk-update
git commit go.mod go.sum -m 'sdk: go: bump engine dependency'
gh pr create
```

## Release Go SDK

```console
export SDK_VERSION=v0.1.0
git tag sdk/go/${SDK_VERSION}
git push origin sdk/go/${SDK_VERSION}
```
