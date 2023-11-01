---
slug: /zenith/developer/191108/advanced-module-development
displayed_sidebar: "zenith"
category: "guides"
authors: ["Vikram Vaswani"]
tags: ["modules"]
date: "2023-03-31"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Advanced Module Development

## Introduction

Once you've understood the basics of writing your own Dagger modules, you're going to inevitably want to learn more and do more. That's where this guide comes in. It shows you some of the more advanced techniques, tips and tricks you will need to supercharge your Dagger module development.

## Requirements

This guide assumes that:

- You have a good understanding of the Dagger Go or Python SDKs. If not, refer to the [Go](https://pkg.go.dev/dagger.io/dagger) or [Python](https://dagger-io.readthedocs.org/) SDK reference.
- You have the Dagger CLI installed. If not, [install Dagger](../../../current/cli/465058-install.md).
- You have a Dagger module. If not, create a module using the [Go](../../developer/quickstarts/525021-go.md) or [Python](../../developer/quickstarts/419481-python.md) quickstarts.
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Use modules in other modules

Modules can call each other. To add a dependency to your module, you can use `dagger mod use`, as in the following example:

```sh
dagger mod use github.com/shykes/daggerverse/helloWorld@26f8ed9f1748ec8c9345281add850fd392441990
```

This module will be added to your `dagger.json`:

```json
  "dependencies": [
    "github.com/shykes/daggerverse/helloWorld@26f8ed9f1748ec8c9345281add850fd392441990"
  ]
```

You can also use local modules as dependencies. However, they must be stored in a sub-directory of your module. For example:

```sh
dagger mod use ./path/to/module
```

The dependent module will be added to your code-generation routines, so you can access it from your own module's code, as shown below:

<Tabs groupId="language">
<TabItem value="Go">

```go
func (m *Potato) HelloWorld(ctx context.Context) (string, error) {
  return dag.HelloWorld().Message(ctx)
}
```

</TabItem>
<TabItem value="Python">

</TabItem>
</Tabs>

:::tip
Find modules on the [Daggerverse](https://daggerverse.dev).
:::

## Chain modules together

Module functions can return custom objects, which in turn can define new functions. This allows for "chaining" of functions in the same style as the [core Dagger API](https://docs.dagger.io/api/reference).

So long as your object can be JSON-serialized by your SDK, its state will be preserved and passed to the next function in the chain.

<Tabs groupId="language">
<TabItem value="Go">

Here is an example module using the Go SDK:

```go
// A Dagger module for saying hello world!

package main

import (
  "context"
  "fmt"
)

type HelloWorld struct {
  Greeting string
  Name     string
}

func (hello *HelloWorld) WithGreeting(ctx context.Context, greeting string) (*HelloWorld, error) {
  hello.Greeting = greeting
  return hello, nil
}

func (hello *HelloWorld) WithName(ctx context.Context, name string) (*HelloWorld, error) {
  hello.Name = name
  return hello, nil
}

func (hello *HelloWorld) Message(ctx context.Context) (string, error) {
  var (
    greeting = hello.Greeting
    name     = hello.Name
  )
  if greeting == "" {
    greeting = "Hello"
  }
  if name == "" {
    name = "World"
  }
  return fmt.Sprintf("%s, %s!", greeting, name), nil
}
```
</TabItem>
<TabItem value="Python">

</TabItem>
</Tabs>

And here is an example query for this module:

```graphql
{
  helloWorld {
    message
    withName(name: "Monde") {
      withGreeting(greeting: "Bonjour") {
        message
      }
    }
  }
}
```

The result will be:

```json
{
  "helloWorld": {
    "message": "Hello, World!",
    "withName": {
      "withGreeting": {
        "message": "Bonjour, Monde!"
      }
    }
  }
}
```

## Miscellanea

- The context and error return are optional in the module's function signature; remove them if you don't need them.
- A module's private fields will not be persisted.

## Conclusion

TODO
