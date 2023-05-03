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

## Podman

### Requirements

This guide assumes that you have Podman installed and running on the host system. If not, [install Podman](https://podman.io/getting-started/installation).

### Configuration

By default, Dagger tries to invoke the `docker` executable. To use Podman instead, create a symbolic link to the Podman executable in your system path and name it `docker`:

```shell
sudo ln -s `which podman` /usr/local/bin/docker
```

:::note
RHEL 8.x users may need to additionally execute `modprobe iptable_nat`.
:::

## Containerd (nerdctl)

### Requirements

This guide assumes that you have `nerdctl` installed and running on the host system in rootless mode. If not, [install the full release of `nerdctl`](https://github.com/containerd/nerdctl/releases) and [configure rootless mode](https://github.com/containerd/nerdctl/blob/main/docs/rootless.md).

### Configuration

By default, Dagger tries to invoke the `docker` executable. To use `nerdctl` instead, create a symbolic link to `nerdctl` in your system path and name it `docker`:

```shell
sudo ln -s `which nerdctl` /usr/local/bin/docker
```

## Conclusion

This guide explained how to use Dagger with various OCI-compatible Docker alternatives, such as Podman and `nerdctl`.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
