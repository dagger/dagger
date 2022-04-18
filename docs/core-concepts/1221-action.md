---
slug: /1221/action
displayed_sidebar: '0.2'
---

# Dagger Actions

Actions are the basic building block of the Dagger platform.
An action encapsulates an arbitrarily complex automation into a simple
software component that can be safely shared, and repeatably executed by any Dagger engine.

Actions can be executed directly with `dagger do`, or integrated as a component of a more complex action.

There are two types of actions: _core actions_ and _composite actions_.

## Core Actions

Core Actions are primitives implemented by the Dagger Engine itself. They can be combined into higher-level composite actions. Their definitions can be imported in the `dagger.io/dagger/core` package.

To learn more about core actions, see [the core action reference](https://github.com/dagger/dagger/tree/main/pkg/dagger.io/dagger/core).

## Composite Actions

Composite Actions are actions made of other actions. Dagger supports arbitrary nesting of actions, so a composite action can be assembled from any combination of core and composite actions.

One consequence of arbitrary nesting is that Dagger doesn't need to distinguish between "pipelines" and "steps": everything is an action. Some actions are just more complex and powerful than others. This is a defining feature of Dagger.

## Lifecycle of an Action

A composite action's lifecycle has 4 stages:

1. Definition
2. Integration
3. Discovery
4. Execution

### Definition

A new action is _defined_ in a declarative template called a [CUE definition](https://cuetorials.com/overview/foundations/#definitions). This definition describes the action's inputs, outputs, sub-actions, and the wiring between them.

Here is an example of a simple action definition:

```cue
package hello

import (
    "dagger.io/dagger"
    "dagger.io/dagger/core"
)

// Write a greeting to a file, and add it to a directory
#AddHello: {
    // The input directory
    dir: dagger.#FS

    // The name of the person to greet
    name: string | *"world"

    write: core.#WriteFile & {
        input: dir
        path: "hello-\(name).txt"
        contents: "hello, \(name)!"
    }

    // The directory with greeting message added
    result: write.output
}
```

Note that this action includes one sub-action: `core.#WriteFile`. An action can incorporate any number of sub-actions.

Also note the free-form structure: an action definition is not structured by a rigid schema. It is simply a CUE struct with fields of various types.

- "inputs" are simply fields which are not complete, and therefore can receive an external value at integration. For example, `dir` and `name` are inputs.
- "outputs" are simply fields which produce a value that can be referenced externally at integration. For example, `result` is an output.
- "sub-actions" are simply fields which contain another action definition. For example, `write` is a sub-action.

There are no constraints to an action's field names or types.

### Integration

Action definitions cannot be executed directly: they must be integrated into a plan.

A plan is an execution context for actions. It specifies:

- What actions to present to the end user
- Dependencies between those tasks, if any
- Interactions between the tasks and the client system, if any

Actions are integrated into a plan by _merging_ their CUE definition into the plan's CUE definition.

Here is an example of a plan:

```cue
package main

import (
    "dagger.io/dagger"
)

dagger.#Plan & {
    // Say hello by writing to a file
    actions: hello: #AddHello & {
        dir: client.filesystem.".".read.contents
    }
    client: filesystem: ".": {
        read: contents: dagger.#FS
        write: contents: actions.hello.result
    }
}
```

Note that `#AddHello` was integrated _directly_ into the plan, whereas `core.#WriteFile` was integrated _indirectly_, by virtue of being a sub-action of `#AddHello`.

To learn more about the structure of a plan, see [it all begins with a plan](./1202-plan.md).

### Discovery

Once integrated into a plan, actions can be discovered by end users, by using the familiar convention of usage messages:

```bash
$ dagger do --help
Execute a dagger action.

Available Actions:
 hello   Say hello by writing to a file

Usage:
  dagger do [OPTIONS] ACTION [SUBACTION...] [flags]

Flags:
  [...]
```

### Execution

Once the end user has discovered the action that they need, they can execute it with `dagger do`. For example:

```bash
dagger do hello
```
