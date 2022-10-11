---
slug: /1231/always-execute
displayed_sidebar: '0.2'
---

# How to always execute an action?

Dagger implemented a way to invalidate cache for a specific action.

The `docker.#Run` and `core.#Exec` actions have an `always` field (which means "always run"):

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

:::caution

Any dependent actions will also be retriggered.

:::
