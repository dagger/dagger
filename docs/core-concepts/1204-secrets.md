---
slug: /1204/secrets
displayed_sidebar: europa
---

# How to use secrets

Most operations in `client` support handling secrets (see [Interacting with the client](./1203-client.md)). More specifically, you can:

- Write a secret to a file;
- Read a secret from a file;
- Read a secret from an environment variable;
- Read a secret from the output of a command;
- Use a secret as the input of a command.

## Environmnet

The simplest use case is reading from an environment variable:

```cue
dagger.#Plan & {
    client: env: GITHUB_TOKEN: dagger.#Secret
}
```

## File

You may need to trim the whitespace, especially when reading from a file:

```cue
dagger.#Plan & {
    // Path may be absolute, or relative to current working directory
    client: filesystem: ".registry": read: {
        // CUE type defines expected content
        contents: dagger.#Secret
    }
    actions: {
        registry: dagger.#TrimSecret & {
            input: client.filesystem.".registry".read.contents
        }
        pull: docker.#Pull & {
            source: "myprivate/image"
            auth: {
                username: "_token_"
                secret: registry.output
            }
        }
    }
}
```

## SOPS

There’s many ways to store encrypted secrets in your git repository. If you use [SOPS](https://github.com/mozilla/sops), here's a simple example where you can access keys from an encrypted yaml file:

```yaml title="secrets.yaml"
myToken: ENC[AES256_GCM,data:AlUz7g==,iv:lq3mHi4GDLfAssqhPcuUIHMm5eVzJ/EpM+q7RHGCROU=,tag:dzbT5dEGhMnHbiRTu4bHdg==,type:str]
sops:
    ...
```

```cue title="main.cue"
dagger.#Plan & {
    client: commands: sops: {
        name: "sops"
        args: ["-d", "./secrets.yaml"]
        stdout: dagger.#Secret
    }

    actions: {
        // Makes the yaml keys easily accessible
        secrets: dagger.#DecodeSecret & {
            input: client.commands.sops.stdout
            format: "yaml"
        }

        run: docker.#Run & {
            mounts: secret: {
                dest:     "/run/secrets/token"
                contents: secrets.output.myToken
            }
            // Do something with `/run/secrets/token`
            ...
        }
    }
}

```
