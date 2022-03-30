---
slug: /1204/secrets
displayed_sidebar: europa
---

# How to use secrets

We can use secrets in a `dagger.#Plan` via the `client` property.
This enables us to:

- read a secret from an environment variable
- read a secret from a file
- read a secret from the output of a command

## Environment variable

The simplest use case is to read secrets from an environment variable:

```cue
dagger.#Plan & {
    client: env: GITHUB_TOKEN: dagger.#Secret
}
```

## File

Some use cases require reading secrets from files that are only readable by specific users:

```cue file=../tests/core-concepts/secrets/plans/file.cue
```

Notice above that we are trimming whitespace via `core.#TrimSecret`

## Command

This example reads a secret token from the output of a [`sops`](https://github.com/mozilla/sops) command:

```cue file=../tests/core-concepts/secrets/plans/sops.cue title="main.cue"
```

```yaml title="secrets.yaml"
myToken: ENC[AES256_GCM,data:AlUz7g==,iv:lq3mHi4GDLfAssqhPcuUIHMm5eVzJ/EpM+q7RHGCROU=,tag:dzbT5dEGhMnHbiRTu4bHdg==,type:str]
sops:
    ...
```

:::tip
With a good understanding of secrets, it is now time to shift our focus to `actions`, packages & imports.
This is where you will be spending most of your time with Dagger.
:::
