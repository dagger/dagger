---
slug: /sdk/cue/233781/secrets
displayed_sidebar: 'current'
---

# How to use secrets

## What are Secrets?

Secrets support in the Dagger CUE SDK allows you to utilize confidential information -- such as passwords, API keys, SSH keys, etc -- when running your Plans, _without_ exposing those secrets in plaintext logs, writing them into the filesystem of containers you're building, or inserting them into cache.

Secrets are never merged into the CUE configuration. They are managed by the Dagger Engine, only  referenced as opaque identifiers, and can only be used by a special filesystem mount or as an environment variable designed to minimize leak risk.

## Getting or Generating Secrets

### Client operations

Most operations in `client` support handling secrets (see [Interacting with the client](./006395-client.md)). More specifically, you can:

- Read a secret from an [environment variable](#read-from-environment);
- Read a secret from a [file](#read-from-file);
- Read a secret from the [output of a command](#sops);

### Read from Environment

The simplest use case is reading from an environment variable:

```cue
dagger.#Plan & {
    client: env: GITHUB_TOKEN: dagger.#Secret
}
```

### Read from File

You may need to trim the whitespace, especially when reading from a file:

```cue file=../tests/core-concepts/secrets/plans/file.cue

```

### SOPS

There’s many ways to store encrypted secrets in your git repository. If you use [SOPS](https://github.com/mozilla/sops), here's a simple example where you can access keys from an encrypted yaml file:

```yaml title="secrets.yaml"
myToken: ENC[AES256_GCM,data:AlUz7g==,iv:lq3mHi4GDLfAssqhPcuUIHMm5eVzJ/EpM+q7RHGCROU=,tag:dzbT5dEGhMnHbiRTu4bHdg==,type:str]
sops: ...
```

```cue file=../tests/core-concepts/secrets/plans/sops.cue title="main.cue"

```

When you pass a file (JSON/YAML) to be encrypted by SOPS, only the values will be encrypted. Follow the steps below to encrypt a `.yaml` file with SOPS and [age](https://github.com/FiloSottile/age):

1.Create an `age` key

```shell
$ age-keygen -o key.txt
Public key: age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p
```

2.Use the public key to encrypt the keys in you yaml file using `sops`.

```shell
sops -e --age age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p unencryted_secrets_sops.yaml  > secrets_sops.yaml
```

3.Edit the `secrets_sops.yaml` using `sops`

```shell
sops secrets_sops.yaml
```

### Exported from Filesystem

In addition, you can export a secret from a filesystem with [`core.#NewSecret`](https://github.com/dagger/dagger/blob/main/pkg/dagger.io/dagger/core/secrets.cue#L22-L33)

This should be used carefully and sparingly, as the source of these secrets will stay in cache.

```cue
package main

import (
  "encoding/yaml"
  "dagger.io/dagger"
  "dagger.io/dagger/core"
)

dagger.#Plan & {
  actions: test: {
    write: core.#WriteFile & {
      input:    dagger.#Scratch
      path:     "/secret"
      contents: yaml.Marshal({
        FOO: "bar"
        ONE: TWO: "twelve"
      })
    }

    secret: core.#NewSecret & {
      input: write.output
      path:  "/secret"
    }

    decode: core.#DecodeSecret & {
      input:    secret.output
      format: "yaml"
    }
  }
}
```

## Using Secrets

Secrets can be used in a number of contexts within a Plan (note: this list is _not exhaustive_):

### In a `Docker.#Run`

As a mounted file

```cue
dagger.#Plan & {
  client: env: GITHUB_TOKEN: dagger.#Secret

  actions: {
    run: docker.#Run & {
      mounts: secret: {
        dest:     "/run/secrets/token"
        contents: client.env.GITHUB_TOKEN
      }
      // Do something with `/run/secrets/token`
    }
  }
}
```

Or as an environment variable

```cue
dagger.#Plan & {
  client: env: GITHUB_TOKEN: dagger.#Secret

  actions: {
    run: docker.#Run & {
      env: GITHUB_TOKEN
      // Do something with `/run/secrets/token`
    }
  }
}
```

### Passed into an Action that utilizes Secrets

```cue
import (
  "dagger.io/dagger"
  "dagger.io/dagger/core"

  "universe.dagger.io/netlify"
)

dagger.#Plan & {
  client: env: NETLIFY_TOKEN: dagger.#Secret
  actions: {
    // Configuration common to all tests
    data: core.#WriteFile & {
      input:    dagger.#Scratch
      path:     "index.html"
      contents: "hello world"
    }

    // Test: deploy a simple site to Netlify
    // Deploy to netlify
    deploy: netlify.#Deploy & {
      team:     "dagger-test"
      token:    client.env.NETLIFY_TOKEN
      site:     "dagger-test"
      contents: data.output
    }
  }
}
```

```cue
dagger.#Plan & {
  client: env: GITHUB_TOKEN: dagger.#Secret

  actions: {
    run: docker.#Run & {
      mounts: secret: {
        dest:     "/run/secrets/token"
        contents: client.env.GITHUB_TOKEN
      }
      // Do something with `/run/secrets/token`
    }
  }
}
```

<!-- TODO: Finish this ### Written to a file on the client -->

## Sharp edges to be aware of

It is possible use Secrets in ways that can risk leaks. Be aware of the risks of these uses, and avoid them if possible.

<!--
TODO: Provide examples of these
- Baking secrets into a container, by copying them into a filesystem or container from a mount or environment variable
-->

We provide safeguards against printing of Secret values to the logs, but you should generally not log Secrets to the console using `echo`, `cat`, etc.

## Safe Transformations of Secrets

### Trim Whitespace

You may need to trim the whitespace, especially when reading from a file:

```cue file=../tests/core-concepts/secrets/plans/file.cue

```
