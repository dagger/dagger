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
  - **peak memory**: difference between the cold and warm workflow-wide peaks;
    only concurrently running containers are added.
  - **net rx / net tx**: received and transmitted bytes saved.

## Expected output

```text
♻️ Cache hits 108/266 (41%) ⚡ Saved ~23s wall · ~2m42s CPU · ~1.2 GB peak memory · ~120 MB net rx · ~31 MB net tx
```

## No-workspace example

- Reset the engine cache using the preparation steps above.

- Run the Alpine module once to record a cold baseline:

  ```bash
  ./bin/dagger api call -m ./modules/alpine container file --path=/bin/busybox digest
  ```

- Run the same workflow again:

  ```bash
  ./bin/dagger api call -m ./modules/alpine container file --path=/bin/busybox digest
  ```

- Explain that `alpine.container` is an existing repository function with no
  `Workspace` argument.

- Point out the higher warm cache hit rate:

  ```text
  ♻️ Cache hits 40/54 (74%) ⚡ Saved ~2s wall · ...
  ```
