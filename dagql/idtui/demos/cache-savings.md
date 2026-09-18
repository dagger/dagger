# Cache savings demo

## Prepare

- Reset the dev engine cache and rebuild:

  ```bash
  docker rm -f dagger-engine.dev
  docker volume rm dagger-engine.dev
  ./hack/build
  ```

## Demo

- Run the workflow once to record a cold baseline:

  ```bash
  ./bin/dagger api call engine-dev network-cidr
  ```

- Explain:
  - **Cache hit rate**: work restored from cache.
  - **Mutualized jobs**: duplicate concurrent work shared by callers.

- Run the same workflow again:

  ```bash
  ./bin/dagger api call engine-dev network-cidr
  ```

- Explain the estimated savings:
  - **Finished faster**: whole-workflow wall-clock savings. Parallel durations
    are not added together.
  - **Compute avoided**: cumulative CPU time across all cores.
  - **Network transfer avoided**: bytes not transferred.
  - **Memory occupancy avoided**: memory integrated over time, not peak memory.

## Expected output

```text
== CACHE ==
Cache hit rate: ...
Mutualized jobs: ...

== ESTIMATED CACHE SAVINGS ==
Compared with a colder run (... cache hit rate)
Finished ~... faster (...%)
Compute avoided: ~... of CPU work
Network transfer avoided: ~...
Memory occupancy avoided: equivalent to ~... held for ...
```
