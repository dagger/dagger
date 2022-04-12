---
slug: /1231/always-execute
displayed_sidebar: europa
---

# Always executing an action

Dagger implemented a way to tell Buildkit not to rely on cache for a specific action.

The `docker.#Run` and `core.#Exec` definitions have an `always` field:

```cue
// If set to true, the cache will never be triggered for that specific action.
always: bool | *false
```

Any package composed on top of it (`bash.#Run` for example) also exposes this field as it will inherit it from `docker.#Run`:

```cue
test: bash.#Run & {
    always: true
    ...
}
```
