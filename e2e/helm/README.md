# Helm E2E Tests

End-to-end tests for Dagger's Helm chart. The chart-only tests lint, render, and package `helm/dagger`; the K3S test installs the chart into an ephemeral cluster and verifies Dagger connectivity through the installed engine.

## How to run

```sh
cd e2e/helm
go test
```

The K3S install test is heavier and can be run separately:

```sh
go test -run TestInstallK3S -count=1
```
