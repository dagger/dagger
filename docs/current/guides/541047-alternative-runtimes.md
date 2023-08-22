---
slug: /541047/alternative-runtimes
displayed_sidebar: 'current'
category: "guides"
tags: ["podman"]
authors: ["Vikram Vaswani"]
date: "2023-04-28"
---

# Use Dagger with Alternative OCI Runtimes

## Introduction

This guide explains how to use Dagger with various OCI-compatible Docker alternatives.

## Approaches

It is possible to run the Dagger Engine container with any other OCI-compatible container runtime, not just those which are CLI-compatible with Docker. There are two possible approaches.

### Use a `docker` symbolic link

By default, Dagger tries to invoke the `docker` executable. To use a different container runtime instead, create a symbolic link to it in your system path and name it `docker`. This approach is suitable for runtimes which are CLI-compatible with Docker.

### Run the Dagger Engine container manually

An alternative approach is to run the Dagger Engine container manually and set the `_EXPERIMENTAL_DAGGER_RUNNER_HOST` environment variable to point to the running container. If this variable is set, then Dagger will instead connect to the endpoint specified there. This approach is suitable for runtimes which are not CLI-compatible with Docker, or when using a customized Dagger Engine container.

The `_EXPERIMENTAL_DAGGER_RUNNER_HOST` variable currently accepts values in the following format:

| Format | Description |
|------- | ------------|
| `docker-container://<container-name>` | Connect to the runner inside the given Docker container. Requires the `docker` CLI to be present and usable. Will result in shelling out to `docker exec`. |
| `podman-container://<container-name>` | Connect to the runner inside the given Podman container. |
| `kube-pod://<pod-name>?context=<context>&namespace=<namespace>&container=<container>` | Connect to the runner inside the given Kubernetes pod. Query strings params like `context` and `namespace` are optional.|
| `unix://<path to unix socket>` | Connect to the runner over the provided UNIX socket. |
| `tcp://<address:port>` | Connect to the runner over TCP using the provided address and port. No encryption is used.

## Runtimes

### Podman

#### Requirements

This guide assumes that you have Podman installed and running on the host system. If not, [install Podman](https://podman.io/getting-started/installation).

#### Configuration

Podman is CLI-compatible with Docker and therefore can be used by creating a symbolic link to the Podman executable in your system path and naming it `docker`:

```shell
sudo ln -s $(which podman) /usr/local/bin/docker
```

:::note
RHEL 8.x users may need to additionally execute `modprobe iptable_nat`.
:::

### Containerd (nerdctl)

#### Requirements

This guide assumes that you have `nerdctl` installed and running on the host system in rootless mode. If not, [install the full release of `nerdctl`](https://github.com/containerd/nerdctl/releases) and [configure rootless mode](https://github.com/containerd/nerdctl/blob/main/docs/rootless.md).

#### Configuration

`nerdctl` is CLI-compatible with Docker and therefore can be used by creating a symbolic link to the `nerdctl` executable in your system path and naming it `docker`.

- To use `nerdctl` directly, create a symbolic link as below:

  ```shell
  sudo ln -s $(which nerdctl) /usr/local/bin/docker
  ```

- To use `nerdctl` via `lima`, create the following shell script at `/usr/local/bin/nerdctl`:

    ```shell
    #!/bin/sh
    lima nerdctl "$@"
    ```

  Then, create a symbolic link to the shell script and name it `docker`:

  ```shell
  sudo ln -s /usr/local/bin/nerdctl /usr/local/bin/docker
  ```

## Conclusion

This guide described two approaches to using Dagger with other OCI-compatible container runtimes, and provided additional steps and guidance for Podman and `nerdctl`.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
