# Testing

## TL;DR

```
# Install dependancies
yarn --cwd . install

# Run all tests
yarn --cwd . test
```

By default, the `dagger` binary is expected to be found in `../cmd/dagger/dagger` relative to the `tests` directory.

If you need to change this, pass along `DAGGER_BINARY=somewhere/dagger`
