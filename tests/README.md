# Testing

## TL;DR

```shell
# Install dependencies
yarn install

# Install gnu parallel if needed
# macOS
brew install parallel
# Debian derivatives
# apt-get install parallel

# Install sops if needed
# macOS
brew install sops

# Run all tests
yarn test
```

By default, the `dagger` binary is expected to be found in `../cmd/dagger/dagger` relative to the `tests` directory.

If you need to change this, pass along `DAGGER_BINARY=somewhere/dagger`

## Run a single test

To run a single test:

```shell
make && ./tests/node_modules/.bin/bats "./tests/<TESTFILE>.bats" -f "<TESTNAME>"
```
