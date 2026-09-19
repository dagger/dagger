# Cache savings demo

## Prepare

- Reset the dev engine cache and rebuild:

  ```bash
  docker rm -f dagger-engine.dev
  docker volume rm dagger-engine.dev
  ./hack/build
  ```

## Workspace example

- Run the workflow once to record a cold baseline:

  ```bash
  ./bin/dagger api call engine-dev network-cidr
  ```

- Point out the cache hit rate: work restored from cache.

- Run the same workflow again:

  ```bash
  ./bin/dagger api call engine-dev network-cidr
  ```

- Point out the estimated savings:
  - **wall**: whole-workflow time saved; parallel durations are not added.
  - **CPU**: CPU time saved across all cores.
  - **memory**: average memory saved for the shown duration, not peak memory.
  - **net rx / net tx**: received and transmitted bytes saved.

## Expected output

```text
♻️ Cache hits 108/266 (41%) ⚡ Saved ~23s wall | ~2m42s CPU | ~335 MB memory for 46s | ~120 MB net rx | ~31 MB net tx
```

## No-workspace example

- Reset the engine cache using the preparation steps above.

- Run the Dang demo once to record a cold baseline:

  ```bash
  ./bin/dagger api call -m ./dagql/idtui/demos/cache-demo cache-savings-demo
  ```

- Run the same workflow again:

  ```bash
  ./bin/dagger api call -m ./dagql/idtui/demos/cache-demo cache-savings-demo
  ```

- Explain that the Dang module uses core API calls and does not use a
  `Workspace`.

- Point out the higher warm cache hit rate:

  ```text
  ♻️ Cache hits 64/94 (68%) ⚡ Saved ~9s wall | ...
  ```
