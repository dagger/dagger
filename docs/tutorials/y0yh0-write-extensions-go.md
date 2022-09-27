---
slug: /y0yh0/write-extensions-go
displayed_sidebar: "0.3"
---

# Write an Extension for a Go Project

Say we are creating a new project called `foo`. It will have

1. A single extension, written in Go, the extends the schema with an action called `bar`.
1. A script, also written in Go, that can call the extension (and any other project dependencies)

## Setup the project configuration

1. Enter a go module directory for your project (`go mod init <module name>` if one doesn't exist)
1. Configure go to use the `cloak` branch in the dagger git repo

   - Then run the following commands to setup the rest of the required dependencies

     ```console
     go get go.dagger.io/dagger@cloak
     # This is needed to fix a transitive dependency issue (`sirupsen` vs. `Sirupsen`...)
     go mod edit -replace=github.com/docker/docker=github.com/docker/docker@v20.10.3-0.20220414164044-61404de7df1a+incompatible
     ```

1. Create a new file called `cloak.yaml`

   - This is where you declare your project, and other project that it depends on. All extensions declared in this file will be built, loaded, and available to be called when the project is loaded into dagger.
   - Create the file in the following format:

   ```yaml
   name: foo
   dependencies:
     - git:
         remote: https://github.com/dagger/dagger.git
         ref: cloak
         path: examples/yarn/cloak.yaml
     - git:
         remote: https://github.com/dagger/dagger.git
         ref: cloak
         path: examples/netlify/go/cloak.yaml
   ```

   - The dependencies are optional and just examples, feel free to change as needed.
   - `core` does not need to be explicitly declared as a dependency; it is implicitly included. If your only dependency is `core`, then you can just skip the `dependencies:` key entirely.

## Create your script

### Implement the script

1. Create `script/main.go`, using the following as a base template, replacing `panic("implement me")` with your implementation

```go
package main

import (
  "context"
  "go.dagger.io/dagger/engine"
)

func main() {
  if err := engine.Start(context.Background(), &engine.Config{}, func(ctx engine.Context) error {
    panic("implement me")
  }); err != nil {
    panic(err)
  }
}
```

1. When you need to call a dependency declared in `cloak.yaml`, you will currently have to use raw graphql queries. Examples of this can be found in [the alpine extension here](https://github.com/dagger/dagger/blob/cloak/examples/alpine/main.go).
1. Also feel free to import any other third-party libraries as needed the same way you would with any other go project.

### Invoke your script

1. If this is the first time running it, you may need a `go mod tidy` to apply the previous go mod commands
1. The simplest way to invoke is to execute `go run script/main.go`

## Create your extension

Update your `cloak.yaml` to include a new `extensions` key:

```yaml
name: foo
extensions:
  - path: ext
    sdk: go
dependencies:
  - git:
      remote: https://github.com/dagger/dagger.git
      ref: cloak
      path: examples/yarn/cloak.yaml
  - git:
      remote: https://github.com/dagger/dagger.git
      ref: cloak
      path: examples/netlify/go/cloak.yaml
```

### Implement the extension

Create `ext/main.go`. Here you will define the API of your extension with structs and attached methods. For example, to define the `foo` API with a `bar` resolver, you may do something like:

```go
package main

import (
  "fmt"
  "strings"

 "go.dagger.io/dagger/sdk/go/dagger"
)

type Foo struct {
}

func (Foo) Bar(ctx dagger.Context, in string) (string, error) {
  return fmt.Sprintf("%s -> %s", in, strings.ToUpper(in)), nil
}

func main() {
  /*
  This registers your API and makes it invokable when imported as a dependency, e.g.

  query {
    foo {
      bar(in: "in")
    }
  }

  would return

  {
    "foo": {
      "bar": "in -> IN"
    }
  }
  */

  dagger.Serve(Foo{})
}
```

Some notes on rules for defining the API

- You may add as many methods as you want.
- Methods are expected to accept `dagger.Context` as their first arg and to return `(<something>, error)`. Methods may accept arbitrary numbers of additional args after `dagger.Context`.
- Args and the return value must be
  - JSON serializable
  - Either primitive values (string, int, float, etc.), slices of those values, or structs of those values (including nesting like slices of structs, structs w/ struct fields, etc.)
  - They currently **cannot** include
    - interface values
    - types with generic type parameters
    - embedded fields
    - unnamed (anonymous) structs
- Methods and struct fields will only be included in the API if they are public (i.e. capitalized). Otherwise they are ignored.

1. When you need to call a dependency declared in `cloak.yaml`, you will currently have to use raw graphql queries. Examples of this can be found in [the alpine extension here](https://github.com/dagger/dagger/blob/cloak/examples/alpine/main.go).
1. Also feel free to import any other third-party dependencies as needed the same way you would with any other go project. They should all be installed and available when executing in the dagger engine.
1. Some examples:
   - [alpine](https://github.com/dagger/dagger/blob/cloak/examples/alpine/main.go)
   - [netlify](https://github.com/dagger/dagger/blob/cloak/examples/netlify/go/main.go)

### Invoke your extension

1. One simple way to verify your extension builds, generates the schema you expect, and can be invoked is via the graphql playground.
   - Just run `dagger dev` from any directory in your project and navigate to `localhost:8080` in your browser (may need [an SSH tunnel](https://www.ssh.com/academy/ssh/tunneling-example) if on a remote host)
     - you can use the `--port` flag to override the port if needed
   - Click the "Docs" tab on the right to see the schemas available, including your extension and any dependencies.
   - You can submit queries by writing them on the left-side pane and clicking the play button in the middle
1. You can also use the dagger CLI, e.g.

```console
dagger do <<'EOF'
{
  foo {
    bar(in: "in")
  }
}
EOF
```

1. Finally, you should now be able to invoke your extension from your script too.
