---
name: integration-tests
description: run daggers integration tests.
---

# Integration Tests

## Overview

The tests under `core/integration/` are dagger's integration tests.

## How to Run Tests

```bash
# Run all test suites
dagger check test-split
```

```bash
# Run all test suites with remote engines
dagger check test-split --scale-out
```

```bash
# List test-split suites
dagger check test-split -l
```

```bash
# Run a specific test suite
dagger check test-split:test-container
```

```bash
# Run a specific test suite using the go toolchain with custom options
dagger call go env with-workdir --path=. with-exec \
--args=go,test,./core/integration,-run,'TestSuiteName',-v,-count,1,-timeout,30m,-parallel,2 \
  --experimental-privileged-nesting stdout
```

## Key Principles

- The tests run their own internal dagger engine, so there is no need to rebuild the outer engine
- The dagger output includes all test output and engine output, including logs, errors, and warnings.
- The output can be quite long, so its best to pipe it to a file or redirect it to a file.
