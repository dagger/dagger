# Testing

## TL;DR

```
# Get help
./test.sh --help

# Run all tests
# You can also just call ./test.sh with no argument
# Also, `make integration` does exactly that
./test.sh all

# Run one random dagger cue directory, with expectations on exit code, stdout, stderr
./test.sh fetch-git/nonexistent/ref --exit=1 --stdout=
```

By default, the dagger binary is expected to be found in ../../cmd/dagger/dagger relative to the test.sh script.

If you need to change this, pass along `DAGGER_BINARY=somewhere/dagger`
