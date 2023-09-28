---
slug: /693013/concurrency
displayed_sidebar: 'current'
category: "guides"
tags: ["go", "python", "nodejs", "concurrency"]
authors: ["Tom Chauveau"]
date: "2023-09-28"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Use Concurrency with Dagger

## Introduction

This guide explains how to use concurrency to increase your Dagger pipeline performance.

The Dagger engine will do what it can to maximize parallelism, but you need to send those API requests in the first place. In common synchronous code, you do one request and wait for it’s response before sending the next request.

There’s an obvious bottleneck here in waiting for the responses, which can take a long time, depending on the workload.
So, whenever it makes sense to run multiple things at the same time, it pays to use your language’s concurrency features (if it supports it) to continue sending more requests while you wait for the responses.

## Requirements

This guide assumes that:

- You have a Go, Python or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have a Dagger SDK installed for one of the above languages. If not, follow the installation instructions for the Dagger [Go](../sdk/go/371491-install.md), [Python](../sdk/python/866944-install.md) or [Node.js](../sdk/nodejs/835948-install.md) SDK.
- You have the Dagger CLI installed in your development environment. If not, [install the Dagger CLI](../cli/465058-install.md).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Approaches

<Tabs groupId="language">
<TabItem value="Go">

You can use [goroutine](https://gobyexample.com/goroutines) in Golang to execute multiple functions in concurrency.

```go file=./snippets/concurrency/main.go
```

</TabItem>
<TabItem value="Node.js">

[Promise](https://basarat.gitbook.io/typescript/future-javascript/promise) in Typescript can be run in concurrency with `Promise.all`.

```typescript file=./snippets/concurrency/index.mts
```

</TabItem>
<TabItem value="Python">

You can create a task group with [anyio](https://anyio.readthedocs.io/en/stable/) to run tasks in concurrency in Python.

```python file=./snippets/concurrency/main.py
```

</TabItem>
</Tabs>

## Conclusion

This guide introduced you to the concurrency with Dagger

Use the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
You can also find real world example of concurrency in our [own CI code](https://github.com/dagger/dagger/blob/475f19908f90f6a5793caf8fa375e2201dc1b1dc/internal/mage/sdk/all.go#L67-L77)
