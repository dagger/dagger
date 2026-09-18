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
Cache hits 108/266 (41%) · Saved ~23s wall | ~2m42s CPU | ~335 MB memory for 46s | ~120 MB net rx | ~31 MB net tx
```

## No-workspace example

- Reset the engine cache using the preparation steps above.

- Move outside the repository so no workspace is discovered:

  ```bash
  dagger_bin="$(realpath ./bin/dagger)"
  cd "$(mktemp -d)"
  ```

- Run this direct core API pipeline twice:

  ```bash
  "$dagger_bin" api call -M container \
    from --address alpine:3.21.3 \
    with-exec --args='sh,-c,apk add --no-cache curl && sleep 2' \
    with-exec --args='curl,--version' \
    with-exec --args='sh,-c,echo cache-demo > /tmp/result' \
    with-exec --args='cat,/tmp/result' \
    stdout
  ```

- Explain that the temporary directory avoids workspace discovery and `-M`
  skips module loading.

- Point out the higher warm cache hit rate:

  ```text
  Cache hits 6/8 (75%) · Saved ~7s wall | ~2.5 MB memory for 7s
  ```
