---
name: dang-dagger-modules
description: Write and edit Dagger modules using the Dang programming language (.dang files). Dang is a statically typed scripting language for GraphQL that serves as a lightweight Dagger SDK. Use when creating, editing, or debugging .dang files for Dagger modules, when a dagger.json references the dang SDK (github.com/vito/dang/dagger-sdk), or when asked to write Dagger module code in Dang. Triggers on .dang files, "write in dang", "dang module", "dang SDK".
---

# Dang Dagger Modules

Dang is a statically typed scripting language for GraphQL, used as a lightweight Dagger SDK. Types and functions are loaded directly from the Dagger GraphQL schema. No codegen phase is needed.

## Project Setup

A Dang Dagger module requires:

1. **`dagger.json`** pointing to the Dang SDK:

```json
{
  "name": "my-module",
  "engineVersion": "v0.19.11",
  "sdk": {
    "source": "github.com/vito/dang/dagger-sdk@be6466632453a52120517e5551c266a239d3899b"
  },
  "dependencies": []
}
```

2. **One or more `.dang` files** (typically `main.dang`). All `.dang` files in the module directory are loaded.

## Language Reference

See [references/language.md](references/language.md) for complete syntax, types, and patterns.

## Dagger Module Patterns

### Basic Module Structure

```dang
pub description = "My module description"

type MyModule {
  """
  A public field exposed as a Dagger function.
  """
  pub greeting: String! = "hello"

  """
  A public method exposed as a Dagger function.
  """
  pub sayHello(name: String!): String! {
    greeting + ", " + name + "!"
  }
}
```

- `type` declares a Dagger object type. The first `type` is the module's main object.
- `pub` fields/methods become Dagger functions. `let` keeps them private.
- `"""..."""` triple-quoted doc strings become Dagger function descriptions.
- `pub description = "..."` at module level sets the module description.

### Constructor Arguments

Fields declared on a type without a body become constructor arguments:

```dang
type Builder {
  pub source: Directory! @defaultPath(path: "/") @ignorePatterns(patterns: [
    "*"
    "!src"
    "!go.mod"
    "!go.sum"
  ])

  pub version: String! = "latest"
}
```

- `@defaultPath(path: "/")` -- load from workspace root by default
- `@ignorePatterns(patterns: [...])` -- filter files (gitignore syntax)
- Default values with `= "latest"`

### Explicit Constructors

Use `new(...)` when constructor args should NOT become fields (e.g. transient inputs, args that need transformation before storage). Args are scoped to the `new()` body and not serialized.

```dang
type MyModule {
  pub source: Directory!

  new(
    src: Directory! @defaultPath(path: "/") @ignorePatterns(patterns: [
      "*"
      "!src"
      "!go.mod"
    ])
  ) {
    self.source = src
  }
}
```

- `new()` args are NOT exposed as fields -- only available inside the body
- Assign fields with `self.field = value`
- `new()` implicitly returns `self`
- Directives (`@defaultPath`, etc.) go on the `new()` args
- Arg names don't need to match field names
- Runtime error if non-null fields aren't assigned
- Types without `new()` derive constructors from fields (existing behavior)

### Container Building (Method Chaining)

```dang
pub build: Container! {
  container
    .from("golang:1.23-alpine")
    .withDirectory("/src", source)
    .withWorkdir("/src")
    .withExec(["go", "build", "-o", "/app", "."])
}
```

Leading dots for method chains. No parens for zero-arg calls (`container.sync` not `container.sync()`).

### Checks and Generators

```dang
"""
Run tests.
"""
pub test: Void @check {
  container
    .from("golang:1.23-alpine")
    .withDirectory("/src", source)
    .withWorkdir("/src")
    .withExec(["go", "test", "./..."])
    .sync

  null
}

"""
Generate client library.
"""
pub generate: Changeset! @generate {
  let updated = container
    .from("node:20-alpine")
    .withDirectory("/src", source)
    .withWorkdir("/src")
    .withExec(["npm", "run", "codegen"])
    .directory("/src")

  updated.changes(source)
}
```

- `@check` marks a function as a check (returns `Void`, use `null` at end)
- `@generate` marks a function as a generator (returns `Changeset!`)
- `Changeset` is produced by `newDir.changes(originalDir)`

### Using Dependencies

Dependencies declared in `dagger.json` are available by name (camelCase):

```json
{
  "dependencies": [
    { "name": "engine-dev", "source": "../engine-dev" },
    { "name": "go", "source": "../go" }
  ]
}
```

```dang
pub binary: File! {
  go(source).binary("./cmd/myapp", noSymbols: true, noDwarf: true)
}

pub test: Void @check {
  engineDev.test(run: "TestMyFeature", pkg: "./core/integration")
}
```

### Workspace API

The Workspace API replaces the legacy `@defaultPath`/`@ignorePatterns` pattern for accessing project files. Instead of eagerly loading directories via constructor fields, functions declare a `ws: Workspace!` argument and call `ws.directory()`/`ws.file()` to lazily access files.

**Key rules:**

- **`Workspace` must be a function argument, never a field.** The engine magically injects it. Storing it in a field is not supported.
- **Any function that takes `ws: Workspace!` is never cached** — the engine can't know in advance which files will be accessed. Design accordingly: keep workspace-dependent functions thin, and push cacheable work into functions that take `Directory!` or `File!` instead.
- **Always filter at the `ws.directory()` call** using `include:`/`exclude:` patterns. Never call `ws.directory(".")` without filters — that eagerly uploads the entire project.
- **Push workspace access to the leaves** when possible. If a function only sometimes needs workspace files, or returns objects that may or may not need them, defer the `ws.directory()` call to the point of actual use. This avoids unnecessary uploads and keeps intermediate results cacheable.

```dang
type MyToolchain {
  pub sourcePath: String! = "sdk/go"

  """
  Get filtered source from the workspace.
  """
  pub source(ws: Workspace!): Directory! {
    ws.directory(sourcePath)
  }

  """
  Bump the SDK version.
  """
  pub bump(ws: Workspace!, version: String!): Changeset! {
    let v = version.trimPrefix("v")
    let contents = "package version\n\nconst Version = \"" + v + "\"\n"
    let workspace = ws.directory(".", include: [
      sourcePath + "/**",
    ])
    workspace
      .withNewFile(sourcePath + "/version.go", contents)
      .changes(workspace)
  }
}
```

#### Lazy workspace access with helper types

When a function discovers multiple items but each item may need workspace files, use a helper type that takes `ws: Workspace!` on its own methods rather than eagerly loading all directories upfront:

```dang
type Discoverer {
  """
  Discover sites by scanning for config files (lightweight).
  """
  pub sites(ws: Workspace!): [Site!]! {
    # Only upload config files for discovery — not the full site dirs
    let configs = ws.directory(".", include: ["**/config.json"])
    let paths = configs.glob("**/config.json")
    paths.map { path =>
      let parts = path.split("/")
      let dirParts = parts.filter { p => p != "config.json" }
      let dirPath = if (dirParts.length == 0) { "." } else { dirParts.join("/") }
      Site(path: dirPath, config: configs.file(path))
    }
  }
}

type Site {
  """
  Path to the site directory, relative to workspace root.
  """
  pub path: String!

  """
  The config file (already loaded, no workspace needed).
  """
  pub config: File!

  """
  The full site directory (lazy — only uploaded when called).
  """
  pub dir(ws: Workspace!): Directory! {
    ws.directory(path)
  }
}
```

This pattern separates cheap discovery (scanning config files) from expensive access (uploading full directories). The caller can inspect `path` and `config` on each `Site` without triggering any large uploads. Only calling `site.dir(ws)` pays the cost, and only for that specific site.

### Control Flow

```dang
# Conditionals
let result = if (condition) { "yes" } else { "no" }

# Multi-line conditionals
if (dryRun == true) {
  ctr = ctr.withExec(["echo", "dry run"])
} else {
  ctr = ctr.withExec(["deploy"])
}

# For loops (mutable accumulator pattern)
let ctr = base
for (pkg in packages) {
  ctr = ctr.withExec(["install", pkg])
}

# Case expressions
let arch = case (defaultPlatform) {
  "linux/amd64" => "x86_64"
  "linux/arm64" => "arm64"
  else => "unknown"
}
```

### Enums

```dang
enum Status {
  PENDING
  RUNNING
  COMPLETED
}

type MyModule {
  pub check(status: Status!): Boolean! {
    status == Status.COMPLETED
  }
}
```

### Error Handling

```dang
pub result = try {
  riskyOperation
} catch {
  err => "fallback: " + err.message
}

raise "something went wrong"
```

## Formatting Rules

- Two-space indentation
- Leading dots for method chains (NOT trailing dots)
- Triple-quoted doc strings (`"""..."""`) — single-quoted `"..."` are NOT doc strings
- No trailing commas in multi-line lists
- No parens for zero-arg calls
- One blank line between type members
- 80 character line limit
- Spaces around operators and after colons
- Named arguments use `:` NOT `=` (e.g. `owner: "root"` not `owner="root"`)
- `Void` return type is never `Void!`

See [references/language.md](references/language.md) Common Pitfalls section for details.

## Editor Setup

See [references/editor-setup.md](references/editor-setup.md) for Zed LSP configuration.
