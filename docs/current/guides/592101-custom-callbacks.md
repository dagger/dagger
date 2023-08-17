---
slug: /592101/custom-callbacks
displayed_sidebar: "current"
category: "guides"
tags: ["nodejs", "go", "python", ]
authors: ["Vikram Vaswani"]
date: "2023-08-18"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Use Custom Callbacks in a Dagger Pipeline

## Introduction

All Dagger SDKs support adding callbacks to the pipeline invocation chain. Using a callback enables greater code reusability and modularity, and also avoids "breaking the chain" when constructing a Dagger pipeline.

This guide explains the basics of creating and using custom callbacks in a Dagger pipeline. You will learn how to:

- Create a custom callback
- Chain the callback into a Dagger pipeline

## Requirements

This tutorial assumes that:

- You have a Go, Python or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have a Dagger SDK installed for one of the above languages. If not, follow the installation instructions for the Dagger [Go](../sdk/go/371491-install.md), [Python](../sdk/python/866944-install.md) or [Node.js](../sdk/nodejs/835948-install.md) SDK.
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Example

All Dagger SDKs support adding a callback via the `With()` API method. The callback must return a function that receives a `Container` from the chain, and returns a `Container` back to it.

Assume that you have the following Dagger pipeline:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/custom-callbacks/mounts-without-callback/main.go
```

Here, the `AddMounts()` function accepts a container, mounts two directories, and returns it to the `main()` function. Within the `main()` function, the call to `AddMounts()` breaks the Dagger pipeline construction chain.

This pipeline can be rewritten to use a callback and the `With()` API method, as below:

```go file=./snippets/custom-callbacks/mounts-with-callback/main.go
```

Here, the `Mounts()` callback function returns a function that receives a `Container` from the chain, and returns a `Container` back to it. It can then be attached to the Dagger pipeline in the normal manner, as an argument to `With()`.

</TabItem>
<TabItem value="Node.js">

```javascript file=./snippets/custom-callbacks/mounts-without-callback/index.mts
```

Here, the `addMounts()` function accepts a container, mounts two directories, and returns the container. Within the main program, the call to `addMounts()` breaks the Dagger pipeline construction chain.

This pipeline can be rewritten to use a callback and the `with()` API method, as below:

```javascript file=./snippets/custom-callbacks/mounts-with-callback/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/custom-callbacks/mounts-without-callback/main.py
```

Here, the `add_mounts()` function accepts a container, mounts two directories, and returns the container. Within the main program, the call to `add_mounts()` breaks the Dagger pipeline construction chain.

This pipeline can be rewritten to use a callback and the `with()` API method, as below:

```python file=./snippets/custom-callbacks/mounts-with-callback/main.py
```

</TabItem>
</Tabs>

Here's another example, this one demonstrating how to add multiple environment variables to a container using a callback:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/custom-callbacks/environment-variables/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./snippets/custom-callbacks/environment-variables/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/custom-callbacks/environment-variables/main.py
```

</TabItem>
</Tabs>

## Conclusion

This guide explained how to create and chain custom callback functions in your Dagger pipeline.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
