---
slug: /1204/secrets
displayed_sidebar: '0.2'
---

# How to use secrets

Most operations in `client` support handling secrets (see [Interacting with the client](./1203-client.md)). More specifically, you can:

- Write a secret to a file;
- Read a secret from a file;
- Read a secret from an environment variable;
- Read a secret from the output of a command;
- Use a secret as the input of a command.

## Environment

The simplest use case is reading from an environment variable:

```cue
dagger.#Plan & {
    client: env: GITHUB_TOKEN: dagger.#Secret
}
```

## File

You may need to trim the whitespace, especially when reading from a file:

```cue file=../tests/core-concepts/secrets/plans/file.cue

```

## SOPS

There’s many ways to store encrypted secrets in your git repository. If you use [SOPS](https://github.com/mozilla/sops), here's a simple example where you can access keys from an encrypted yaml file:

```yaml title="secrets.yaml"
myToken: ENC[AES256_GCM,data:AlUz7g==,iv:lq3mHi4GDLfAssqhPcuUIHMm5eVzJ/EpM+q7RHGCROU=,tag:dzbT5dEGhMnHbiRTu4bHdg==,type:str]
sops: ...
```

```cue file=../tests/core-concepts/secrets/plans/sops.cue title="main.cue"

```
