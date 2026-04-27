# Installer E2E Tests

End-to-end tests for the Dagger bash installer script (`install.sh`). Each test runs the installer in a container and verifies the installed binary works. See `installers_test.go` for the full list of test cases.

## How to run

```sh
cd e2e/installers
go test
```

For pretty TUI output:

```
dagger run go test
```
