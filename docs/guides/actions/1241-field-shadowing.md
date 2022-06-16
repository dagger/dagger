---
slug: /1241/field-shadowing
displayed_sidebar: "0.2"
---

# Understanding field shadowing and how to avoid it

Field shadowing is a common CUE error than can lead your plan to unexpected behavior really painful to debug.

:::info
Before reading this page, we recommend you to read [CUE Guide](../../core-concepts/1215-what-is-cue.md).
:::

## What is field shadowing ?

It happens whenever you are using the same name as a key and value where this value is define at outer scope.  
A concrete example is the best way to understand field shadowing

```cue
// We define key test with value "hello world"
test: "hello world"

// We define a simple definition with field test
#Def: {
   test: string
}

// We concretise our definition and assign key test to value defined in 
// outer key test
// This will produce a shodowing 
shadow: #Def & {
   test: test
}

// We concretise our definition and assign key test to value defined in 
// outer key test but we resolve shadowing by encapsulate key with quote.
concrete: #Def & {
  "test": test
}

// Result is
test: "hello world"
shadow: {
  test: string // test is still a string even if we set the key because it gets shadowed
}
concrete: {
  test: "hello world" // test is concrete there because we resolve shadowing with quote
}
```

You can see this example directly on CUE Playground [here](https://cuelang.org/play/?id=g8h7a6AfZN7#cue@export@cue).

As you understood, for values at the same scope, sometime CUE has a hard time
knowing whether you mean the other value or the key inside a definition.
Putting quotes around the key when it has the same name as the value ensure
CUE will not get confused, and you meet the expected behavior.

## How to avoid it

Here's a concrete example of shadowing that can happens with Dagger

```cue
package main

import (
    "dagger.io/dagger"
    "universe.dagger.io/docker"
    "universe.dagger.io/alpine"
)

dagger.#Plan & {
  // Define a simple hello action
  actions: hello: {
    _image: alpine.#Build

    MESSAGE: "hello dagger"

    run: docker.#Run & {
      input: _image.output
      command: {
        name: "/bin/sh"
        args: ["-c", "echo $MESSAGE"]
      }
      env: MESSAGE: MESSAGE
    }
  }
}
```

If we execute this one, it will fail because `MESSAGE` has a conflict created
from field shadowing.

```shell
dagger do hello                   
[✗] actions.hello.run                                                      0.0s
[✔] actions.hello                                                          0.0s
12:12PM FTL failed to execute plan: task failed: actions.hello.run._exec: actions.hello.run._exec.env.MESSAGE: non-concrete value (string|struct)
```

To remove this error, just wrap key `MESSAGE` with quotes and plan will
successfully run.

```cue
run: docker.#Run & {
            input: _image.output
            command: {
                name: "/bin/sh"
                args: ["-c", "echo $MESSAGE"]
            }
            env: "MESSAGE": MESSAGE
        }
```

```shell
dagger do hello
[✔] actions.hello.run                                                      0.0s
[✔] actions.hello                                                          0.0s
```
