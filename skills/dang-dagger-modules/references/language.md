# Dang Language Reference

## Table of Contents

- [Type System](#type-system)
- [Declarations](#declarations)
- [Expressions](#expressions)
- [Control Flow](#control-flow)
- [Operators](#operators)
- [String Methods](#string-methods)
- [List Methods](#list-methods)
- [Directives](#directives)
- [Enums and Scalars](#enums-and-scalars)
- [Interfaces](#interfaces)
- [Error Handling](#error-handling)
- [Testing](#testing)
- [Dagger API Globals](#dagger-api-globals)
- [Complete Examples](#complete-examples)

## Type System

### Primitive Types

| Type | Non-null | Description |
|------|----------|-------------|
| `String` | `String!` | Text value |
| `Int` | `Int!` | Integer value |
| `Boolean` | `Boolean!` | `true` or `false` |
| `Void` | - | No value (use `null` to return) |

### Nullability

- `Type!` -- non-null, guaranteed to have a value
- `Type` -- nullable, may be `null`
- `String!` satisfies `String`, but `String` does not satisfy `String!`
- `??` operator for null coalescing: `value ?? "default"`

#### Nullable Propagation

Accessing a field or calling a method on a nullable receiver always produces a nullable result, even if the field is declared `Type!`. This is **contagious nullability**:

```dang
let json: JSONValue   # nullable
json.field(["name"])  # nullable JSONValue (not JSONValue!)
  .asString           # nullable String (not String!)
```

The error `cannot use String as String!` means you have a nullable `String` where a non-null `String!` is expected.

#### Flow-Sensitive Null Narrowing

Inside an `if` block that checks for null, the type system narrows the variable to non-null:

```dang
let x: String   # nullable

if (x != null) {
  # x is String! here — safe to use where String! is required
  x.split("/")
}

if (x == null) {
  "default"
} else {
  # x is String! in the else branch
  x.split("/")
}
```

Both `x == null` and `null == x` forms are supported, as are `!=` equivalents.

**Limitations:**

- Only simple `x == null` and `x != null` conditions narrow types
- Compound conditions with `and`/`or` do not narrow
- Boolean negation `!` does not narrow
- Only direct variable references are narrowed (not `a.b` or function calls)
- Narrowing is single-level: checking `obj != null` narrows `obj`, but fields accessed on `obj` still follow normal nullability rules

### Composite Types

- Lists: `[String!]!`, `[Int!]!`, `[Container!]!`
- Dagger types: `Container!`, `Directory!`, `File!`, `Secret!`, `Service!`, `CacheVolume!`, `GitRepository!`
- Module-defined types: any `type` declaration
- `Changeset!` -- file diff between two directories

## Declarations

### Module-Level

```dang
# Module description
pub description = "My module"

# Public binding (exposed to Dagger)
pub myValue: String! = "hello"

# Private binding
let helper: String! = "internal"
```

### Type (Object) Declaration

```dang
type MyType {
  # Public field (constructor arg if no body)
  pub name: String!

  # Public field with default
  pub version: String! = "1.0"

  # Private field
  let internal: Int! = 0

  # Public computed field (Dagger function)
  pub greeting: String! {
    "Hello, " + name
  }

  # Public method with args (Dagger function)
  pub build(target: String!): File! {
    container
      .from("alpine")
      .withExec(["build", target])
      .file("/output")
  }

  # Private method
  let helperMethod: Container! {
    container.from("alpine")
  }
}
```

### Explicit Constructor

Use `new(...)` to define constructor args that are NOT fields. Args are only available inside the `new()` body and are not serialized. This is important for Dagger modules where constructor inputs shouldn't be persisted.

```dang
type Greeter {
  pub greeting: String!

  new(name: String!) {
    self.greeting = "Hello, " + name + "!"
  }
}

# Greeter("World").greeting == "Hello, World!"
```

Rules:
- `new()` args are scoped to the body — not accessible in methods
- Assign fields with `self.field = value`
- `new()` implicitly returns `self`
- Runtime error if non-null fields aren't assigned
- Directives go on `new()` args: `new(src: Directory! @defaultPath(path: "/"))`
- Arg names don't need to match field names
- Only one `new()` per type; only valid inside type bodies
- Types without `new()` derive constructors from fields (default behavior)

### Variable Binding

```dang
let x = 42
let name: String! = "hello"
let items = ["a", "b", "c"]

# Reassignment
let ctr = container.from("alpine")
ctr = ctr.withExec(["echo", "hi"])
```

## Expressions

### Literals

```dang
"hello"           # String
42                # Int
true              # Boolean
null              # Null
["a", "b", "c"]   # List
```

### Function/Method Calls

```dang
# Positional arguments
container.from("alpine")

# Named arguments
container.withExec(["echo"], expect: ReturnType.ANY)

# Mixed (first arg positional, rest named)
go(source).binary("./cmd/app", noSymbols: true)

# Zero-arg calls: no parens needed
container.sync     # not container.sync()
source.entries     # not source.entries()

# Method chaining with leading dots
container
  .from("alpine")
  .withDirectory("/src", source)
  .withWorkdir("/src")
  .withExec(["make", "build"])
```

### String Operations

```dang
# Concatenation
"hello" + " " + "world"

# String interpolation is NOT supported -- use concatenation
let msg = "Version: " + version

# Multi-line strings (no special syntax, use \n)
let content = "line1\nline2\nline3\n"
```

### List Operations

```dang
let items = ["a", "b", "c"]
let combined = items + ["d", "e"]    # List concatenation
let first = items[0]                  # Indexing

# Multi-line lists (no trailing commas)
let packages = [
  "bash"
  "git"
  "curl"
]
```

### Multi-Field Selection

```dang
# Fetch multiple fields in one query
let data = users.{name, email}
let nested = users.{name, posts.{title, createdAt}}
```

### Object Literals

```dang
let config = {{
  name: "test"
  port: 8080
  items: ["a", "b"]
}}
```

### Type Casting

```dang
let typed = []::[String!]!    # Empty list with type annotation
```

## Control Flow

### Conditionals

```dang
# Inline
let status = if (enabled) { "active" } else { "inactive" }

# Multi-line
if (condition) {
  doSomething
} else if (other) {
  doOther
} else {
  fallback
}
```

### For Loops

```dang
# Accumulator pattern (returns final value of ctr)
let ctr = base
for (pkg in packages) {
  ctr = ctr.withExec(["apk", "add", pkg])
}
ctr
```

### Case Expressions

```dang
let arch = case (platform) {
  "linux/amd64" => "x86_64"
  "linux/arm64" => "aarch64"
  else => "unknown"
}
```

## Operators

| Operator | Description |
|----------|-------------|
| `+` | Addition / string concat / list concat |
| `-`, `*`, `/` | Arithmetic |
| `==`, `!=` | Equality |
| `<`, `>`, `<=`, `>=` | Comparison |
| `and`, `or` | Boolean logic |
| `!` | Boolean negation |
| `??` | Null coalescing |

## String Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `trimPrefix` | `(prefix: String!): String!` | Remove prefix |
| `trimSuffix` | `(suffix: String!): String!` | Remove suffix |
| `trimRight` | `(cutset: String!): String!` | Trim right chars |
| `split` | `(sep: String!): [String!]!` | Split string |
| `join` | on `[String!]!`, `(sep: String!): String!` | Join list of strings |
| `toUpper` | `(): String!` | Uppercase |
| `toLower` | `(): String!` | Lowercase |
| `contains` | `(sub: String!): Boolean!` | Substring check |
| `match` | `(pattern: String!): Boolean!` | Regex match |

## List Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `length` | field, no parens | List length |
| `map` | `{ item => expr }` | Transform elements |
| `filter` | `{ item => bool }` | Filter elements |
| `join` | `(sep: String!): String!` | Join strings |
| `dropLast` | `(count: Int! = 1)` | Remove last N elements (auto-callable, no parens needed) |
| `dropFirst` | `(count: Int! = 1)` | Remove first N elements (auto-callable) |
| `takeLast` | `(count: Int! = 1)` | Keep only last N elements (auto-callable) |
| `takeFirst` | `(count: Int! = 1)` | Keep only first N elements (auto-callable) |
| `dropWhile` | `{ item => bool }` | Drop leading elements while predicate is true |
| `takeWhile` | `{ item => bool }` | Take leading elements while predicate is true |

## Directives

### On Constructor Arguments

```dang
# Default path for Directory/File args
pub source: Directory! @defaultPath(path: "/")

# Ignore patterns (gitignore syntax)
pub source: Directory! @defaultPath(path: "/") @ignorePatterns(patterns: [
  "*"
  "!src"
  "!*.go"
])

# Positional shorthand
pub source: Directory! @defaultPath("/")
```

### On Functions

```dang
# Mark as a check function
pub test: Void @check { ... }

# Mark as a generator function
pub generate: Changeset! @generate { ... }
```

### Declaring Custom Directives

```dang
directive @deprecated(reason: String!) on FIELD_DEFINITION
directive @experimental on FIELD_DEFINITION
```

## Enums and Scalars

### Enum Declaration

```dang
enum Priority {
  LOW
  MEDIUM
  HIGH
}

# Usage
let p = Priority.HIGH
if (p == Priority.HIGH) { "urgent" } else { "normal" }
```

### Scalar Declaration

```dang
scalar Timestamp
scalar URL
```

Scalars are opaque string-backed types.

## Interfaces

```dang
interface Named {
  pub name: String!
}

type Person implements Named {
  pub name: String!
  pub age: Int!
}

type Bot implements Named & Configurable {
  pub name: String!
  pub config: String!
}
```

## Error Handling

```dang
# Try/catch
let result = try {
  riskyOperation
} catch {
  v: ValidationError => "invalid: " + v.field
  err => "error: " + err.message
}

# Raise errors
raise "something went wrong"
raise Error(message: "not found")

# Custom error types
type NotFoundError implements Error {
  pub message: String!
  pub resource: String!
}

raise NotFoundError(message: "missing", resource: "user")
```

## Testing

```dang
# Assertions (for testing, not exposed to Dagger)
assert { 1 + 1 == 2 }
assert { "hello".toUpper == "HELLO" }
assert { [1, 2, 3].length == 3 }
```

## Dagger API Globals

These are available in all Dang Dagger modules from the Dagger GraphQL schema:

| Global | Type | Description |
|--------|------|-------------|
| `container` | `Container!` | Create new container |
| `directory` | `Directory!` | Create empty directory |
| `cacheVolume(key)` | `CacheVolume!` | Named cache volume |
| `setSecret(name, plaintext)` | `Secret!` | Create a secret |
| `currentModule` | `CurrentModule!` | Current module info |
| `defaultPlatform` | `String!` | Host platform |
| `llm` | `LLM!` | LLM agent |
| `env` | `Env!` | Environment |

Container chaining methods include: `.from()`, `.withExec()`, `.withDirectory()`, `.withFile()`, `.withWorkdir()`, `.withEnvVariable()`, `.withSecretVariable()`, `.withMountedCache()`, `.withMountedSecret()`, `.withNewFile()`, `.withEntrypoint()`, `.withDefaultArgs()`, `.file()`, `.directory()`, `.sync`, `.exitCode`, `.stdout`, `.stderr`.

Directory methods include: `.file()`, `.directory()`, `.entries`, `.glob(pattern)`, `.withFile()`, `.withDirectory()`, `.withNewFile()`, `.withoutDirectory()`, `.dockerBuild`, `.changes()`.

Workspace methods include: `.directory(path, include?, exclude?)`, `.file(path)`. Workspace is always a function argument, never a field. See skill.md for Workspace API details.

## Common Pitfalls

### Named arguments use `:` not `=`

Function call arguments use `:` to separate key from value. Using `=` is a parse error — the parser interprets `=` as reassignment, breaking the argument list.

```dang
# CORRECT
container.withMountedDirectory(".", src, owner: "nonroot")
go(source).binary("./cmd/app", noSymbols: true)

# WRONG — parse error
container.withMountedDirectory(".", src, owner="nonroot")
```

Note: `=` is used in declarations (`pub x = 1`, default values `version: String! = "latest"`), but never in function call arguments.

### Doc strings must be triple-quoted

Only `"""..."""` triple-quoted strings are parsed as doc strings and attached to declarations. Single-quoted `"..."` strings before a declaration are standalone expressions — they parse fine but produce no documentation.

```dang
# CORRECT — attached as Dagger function description
"""
Lint the code.
"""
pub lint: Void @check { ... }

# WRONG — parses as a standalone string, not a doc string
"Lint the code."
pub lint: Void @check { ... }
```

### `Void` has no non-null form

`Void` is the return type for side-effect-only functions. Do not write `Void!` — while it parses, it is semantically incorrect. Always use bare `Void`.

```dang
# CORRECT
pub test: Void @check { ... }

# WRONG
pub test: Void! @check { ... }
```

### Trailing dots break method chains

Method chains use **leading** dots. Trailing dots (dot at end of line) are technically valid but non-idiomatic and error-prone when editing.

```dang
# CORRECT — leading dots
container
  .from("alpine")
  .withWorkdir("/app")

# WRONG — trailing dots (fragile, hard to read)
container.
  from("alpine").
  withWorkdir("/app")
```

### Nullable propagation through field access

Accessing a field on a nullable value makes the result nullable, even if the field is `Type!`. This is the most common source of type errors.

```dang
# WRONG — json is nullable, so .field() and .asString return nullable types
pub packageManager: String! {
  json.field(["packageManager"]).asString
  # Error: cannot use String as String!
}

# RIGHT — guard against null first, then access fields in the non-null branch
pub packageManager: String! {
  if (json == null) {
    "npm"
  } else {
    json.field(["packageManager"]).asString ?? "npm"
  }
}
```

The pattern: check the nullable receiver for null **before** accessing its fields. Inside the non-null branch, `json` is narrowed to non-null, so field access produces non-null results.

## Complete Examples

### Simple Build Module

```dang
pub description = "Build a Go application"

type GoBuild {
  pub source: Directory! @defaultPath(path: "/") @ignorePatterns(patterns: [
    "*"
    "!**/*.go"
    "!go.mod"
    "!go.sum"
  ])

  let base: Container! {
    container
      .from("golang:1.23-alpine")
      .withMountedCache("/go/pkg/mod", cacheVolume("go-mod"))
      .withDirectory("/src", source)
      .withWorkdir("/src")
  }

  """
  Build the application binary.
  """
  pub build: File! {
    base
      .withExec(["go", "build", "-o", "/app", "."])
      .file("/app")
  }

  """
  Run tests.
  """
  pub test: Void @check {
    base
      .withExec(["go", "test", "-v", "./..."])
      .sync

    null
  }

  """
  Lint the code.
  """
  pub lint: Void @check {
    base
      .withExec(["go", "vet", "./..."])
      .sync

    null
  }
}
```

### SDK Development Toolchain

```dang
pub description = "Develop the Foo SDK"

type FooSdkDev {
  pub workspace: Directory! @defaultPath(path: "/") @ignorePatterns(patterns: [
    "*"
    "!sdk/foo"
  ])

  pub sourcePath: String! = "sdk/foo"

  pub source: Directory! {
    workspace.directory(sourcePath)
  }

  let devContainer: Container! {
    container
      .from("node:20-alpine")
      .withMountedCache("/cache/npm", cacheVolume("npm-cache"))
      .withWorkdir("/app")
      .withDirectory(".", workspace)
      .withExec(["npm", "--prefix", sourcePath, "install"])
  }

  """
  Run SDK tests.
  """
  pub test: Void @check {
    devContainer
      .withExec(["npm", "--prefix", sourcePath, "test"])
      .sync

    null
  }

  """
  Lint the SDK.
  """
  pub lint: Void @check {
    devContainer
      .withExec(["npm", "--prefix", sourcePath, "run", "lint"])
      .sync

    null
  }

  """
  Bump the SDK engine dependency version.
  """
  pub bump(version: String!): Changeset! {
    let v = version.trimPrefix("v")
    let contents = "export const ENGINE_VERSION = \"" + v + "\"\n"
    workspace
      .withNewFile(sourcePath + "/src/version.ts", contents)
      .changes(workspace)
  }

  """
  Release the SDK.
  """
  pub release(
    sourceTag: String!,
    npmToken: Secret!,
    dryRun: Boolean,
  ): Void {
    let version = sourceTag.trimPrefix(sourcePath + "/v")
    let build = devContainer
      .withWorkdir(sourcePath)
      .withExec(["npm", "run", "build"])
      .withExec(["npm", "version", version.trimPrefix("v")])

    let publishArgs = ["npm", "publish", "--access", "public"]
    if (dryRun == true) {
      build.withExec(publishArgs + ["--dry-run"]).sync
    } else {
      build
        .withSecretVariable("NPM_TOKEN", npmToken)
        .withExec(publishArgs)
        .sync
    }

    null
  }
}
```
