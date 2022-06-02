---
slug: /1231/always-execute
displayed_sidebar: '0.2'
---

# How to always execute an action ?

Dagger implemented a way invalidate cache for a specific action.

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

:::warning
Any dependent actions will also be retriggered
:::
