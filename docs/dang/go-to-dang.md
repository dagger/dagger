# Porting Dagger Modules from Go to Dang

This manual provides practical guidance for transpiling Dagger modules from Go to Dang, based on hands-on experience with the git-releaser module.

## Table of Contents

1. [Overview](#overview)
2. [Type System](#type-system)
3. [Module Structure](#module-structure)
4. [Functions and Methods](#functions-and-methods)
5. [GraphQL Directives](#graphql-directives)
6. [Control Flow](#control-flow)
7. [String Operations](#string-operations)
8. [Dagger API Patterns](#dagger-api-patterns)
9. [Error Handling](#error-handling)
10. [Known Limitations](#known-limitations)
11. [Common Patterns](#common-patterns)

---

## Overview

Dang is a statically typed language designed specifically for scripting GraphQL APIs, with native support for Dagger. Key advantages over Go for Dagger modules:

- No code generation phase needed
- Types loaded directly from GraphQL schema
- Simpler, more concise syntax
- Perfect for "glue code" that combines existing modules

**Philosophy:** Dang prioritizes familiarity over theory, ergonomics over syntactic purity, and expressiveness over performance.

---

## Type System

### Basic Type Mappings

| Go | Dang | Notes |
|---|---|---|
| `string` | `String` | Nullable by default |
| `int` | `Int!` | Non-null integer |
| `bool` | `Boolean!` | Non-null boolean |
| `*dagger.File` | `File` | Nullable |
| `*dagger.Directory` | `Directory!` | Usually non-null |
| `*dagger.Secret` | `Secret` | Core Dagger type |
| `*dagger.Container` | `Container!` | Core Dagger type |
| `*dagger.GitRepository` | `GitRepository!` | Core Dagger type |
| `[]string` | `[String!]!` | Non-null list of non-null strings |
| `error` | `Void` | No explicit error returns |

### Null Tracking

Dang has sophisticated null tracking:

```dang
# String (nullable) does NOT satisfy String! (non-null)
# But String! (non-null) DOES satisfy String (nullable)

let maybeValue: String = null  # OK
let definiteValue: String! = "hello"  # OK
```

### Flow-Sensitive Null Checking

```dang
let value: String = getSomeValue()

if value != null {
  # Inside this block, value is treated as String! (non-null)
  let length = value.length  # OK
}
```

### Enums

Dang supports enum types with dot notation for accessing values:

**Defining enums:**
```dang
enum Status {
  PENDING
  COMPLETED
  FAILED
}
```

**Accessing enum values:**
```dang
# Go: return CheckCompleted, nil
# Dang: return Status.COMPLETED
let status = Status.COMPLETED
```

**Using enums in return types:**
```dang
pub getStatus(): Status! {
  Status.COMPLETED
}
```

---

## Module Structure

### Go Module

```go
package main

import (
    "context"
    "dagger/my-module/internal/dagger"
)

type MyModule struct {
    Version string // +private
}

func New(
    // +default="1.0.0"
    // +optional
    version string,
) *MyModule {
    return &MyModule{
        Version: version,
    }
}

func (m *MyModule) Build(ctx context.Context, source *dagger.Directory) (*dagger.File, error) {
    // implementation
}
```

### Dang Module

```dang
type MyModule {
  """
  Source directory (public field)
  """
  pub source: Directory!

  """
  Version of the module (private field)
  """
  let version: String! = "1.0.0"

  """
  Build the source code
  """
  pub build(): File! {
    # implementation
  }
}
```

### Key Differences

1. **No package/import statements** - Dependencies are automatically available from GraphQL schema
2. **Constructor is implicit** - Type definition creates constructor automatically
3. **Field visibility** - Use `pub` for public fields, `let` for private fields
4. **Methods use `pub`** - Public methods use `pub` keyword
5. **No context parameter** - Context is implicit in Dang
6. **No error returns** - Operations either succeed or fail (no explicit error handling)
7. **Docstrings use `"""`** - Triple-quoted strings for documentation

---

## Functions and Methods

### Function Syntax

**Go:**
```go
func (m *MyModule) ProcessFile(
    ctx context.Context,
    input *dagger.File,
    // +optional
    format string,
) (*dagger.File, error) {
    if format == "" {
        format = "json"
    }
    // ...
}
```

**Dang:**
```dang
pub processFile(
  input: File!,
  format: String! = "json",
): File! {
  # ...
}
```

### Default Arguments

**Go uses comments:**
```go
// +default="value"
// +optional
param string
```

**Dang uses inline syntax:**
```dang
param: String! = "value"
```

### Parameter Documentation

Dang supports two styles for documenting function parameters:

**Triple-quoted docstrings (preferred for public APIs):**
```dang
pub myFunc(
  """
  The source directory to process
  """
  source: Directory!,
  """
  Optional output format
  """
  format: String! = "json"
): File! {
  # ...
}
```

**Inline comments (more concise):**
```dang
pub myFunc(
  # The source directory to process
  source: Directory!,
  # Optional output format
  format: String! = "json"
): File! {
  # ...
}
```

Both styles are valid and can be mixed within the same module.

### Optional vs Required Parameters

**Go:**
```go
func MyFunc(
    required string,
    optional string, // +optional
) {}
```

**Dang:**
```dang
pub myFunc(
  required: String!,  # Non-null = required
  optional: String = "",  # Nullable with default = optional
): Void {}
```

### Return Types

- Go functions that return `error` become Dang functions returning `Void`
- Go functions that return `(T, error)` become Dang functions returning `T!` or `T`
- Always return `null` at the end of `Void` functions

**Example:**
```dang
pub myFunc(): Void {
  container.withExec(["echo", "hello"]).sync
  null  # Must explicitly return null for Void functions
}
```

---

## GraphQL Directives

Dang supports GraphQL directives, which provide a way to add metadata to type and field definitions. Directives are the Dang equivalent of Go's comment-based annotations.

### Dagger Directives

**Common Dagger directives:**

| Go Annotation | Dang Directive | Purpose |
|---|---|---|
| `// +defaultPath="/"` | `@defaultPath(path: "/")` | Default path for Directory/File parameters |
| `// +ignore=[...]` | `@ignorePatterns(patterns: [...])` | Patterns to ignore when loading directories |
| `// +default="value"` | N/A | Use inline default: `param: String! = "value"` |
| `// +optional` | N/A | Make type nullable: `param: String = null` |

### Directive Syntax

Directives are applied using the `@` symbol followed by the directive name and arguments:

```dang
# Named arguments
@directiveName(arg1: value1, arg2: value2)

# Positional arguments (more concise)
@directiveName(value1, value2)

# Mixed (positional first, then named)
@directiveName(value1, arg2: value2)
```

### Examples

**Go with annotations:**
```go
func New(
    // +defaultPath="/"
    // +ignore=[
    //  "*",
    //  "!sdk/go",
    //  "!**/go.mod",
    //  "!**/go.sum"
    // ]
    workspace *dagger.Directory,
    // +default="sdk/go"
    sourcePath string,
) *MyModule {
    return &MyModule{
        Workspace: workspace,
        SourcePath: sourcePath,
    }
}
```

**Dang with directives:**
```dang
type MyModule {
  # Using named arguments (explicit)
  pub workspace: Directory! @defaultPath(path: "/") @ignorePatterns(patterns: [
    "*"
    "!sdk/go"
    "!go.mod"
    "!go.sum"
    "!/cmd/codegen"
    "!/engine/slog"
  ])

  # Or using positional arguments (more concise)
  # pub workspace: Directory! @defaultPath("/") @ignorePatterns([
  #   "*"
  #   "!sdk/go"
  #   "!go.mod"
  #   "!go.sum"
  #   "!/cmd/codegen"
  #   "!/engine/slog"
  # ])

  pub sourcePath: String! = "sdk/go"
}
```

### Directives on Function Parameters

Directives can also be applied to function parameters:

**Go:**
```go
func (m *MyModule) Build(
    // +defaultPath="/"
    source *dagger.Directory,
    // +defaultPath="./Dockerfile"
    dockerfile *dagger.File,
) *dagger.Container {
    // ...
}
```

**Dang:**
```dang
pub build(
  # Named syntax
  source: Directory! @defaultPath(path: "/"),
  # Positional syntax (more concise)
  dockerfile: File! @defaultPath("./Dockerfile"),
): Container! {
  # ...
}
```

### Multiple Directives

You can apply multiple directives to the same field or parameter:

```dang
let source: Directory! @defaultPath(path: "/") @ignorePatterns(patterns: [
  ".git"
  "node_modules"
  "*.log"
])
```

### Important Notes

1. **Flexible argument syntax**: Directive arguments can be positional, named, or mixed:
   - Positional: `@defaultPath("/")`
   - Named: `@defaultPath(path: "/")`
   - Mixed: `@directive(arg1, name: value)` (positional args must come first)
   - **Why?** GraphQL arguments are both named *and* ordered, so Dang supports both calling styles
2. **Array syntax**: Arrays in directives use the same syntax as Dang arrays (no commas between elements on separate lines)
3. **No commas in multiline arrays**: When spreading array elements across lines, don't use commas
4. **String literals**: Use double quotes for strings

---

## Control Flow

### If/Else

**Go:**
```go
if condition {
    // ...
} else if other {
    // ...
} else {
    // ...
}
```

**Dang:**
```dang
if condition {
  # ...
} else if other {
  # ...
} else {
  # ...
}
```

**Inline conditionals:**
```dang
let result = if condition { "yes" } else { "no" }
```

### For Loops

**Go:**
```go
for i, item := range items {
    // ...
}
```

**Dang:**
```dang
for index, item in items {
  # ...
}

# Without index:
for item in items {
  # ...
}
```

### Variable Assignment and Mutation

**Dang uses `let` for declarations, allows reassignment:**
```dang
let filterArgs = ["git", "filter-repo"]
filterArgs = filterArgs + ["--force"]  # Reassignment OK

# Conditional reassignment:
if condition {
  filterArgs = filterArgs + ["--extra-flag"]
}
```

---

## String Operations

Dang provides rich string methods as part of the standard library:

### Available String Methods

```dang
# Trimming
"  hello  ".trimSpace()              # → "hello"
"/path/to/file".trimPrefix("/path")  # → "/to/file"
"file.txt".trimSuffix(".txt")        # → "file"
"!!!hello!!!".trim("!")              # → "hello"

# Checking
"hello world".contains("world")      # → true
"hello".hasPrefix("he")              # → true
"hello".hasSuffix("lo")              # → true

# Case conversion
"hello".toUpper()  # → "HELLO"
"HELLO".toLower()  # → "hello"

# Splitting
"a,b,c".split(",")  # → ["a", "b", "c"]

# Padding
"hi".padLeft(5)    # → "   hi"
"hi".padRight(5)   # → "hi   "
"hi".center(5)     # → " hi  "
```

### String Concatenation

```dang
let url = "https://example.com/" + version + "/file"

# For format strings with multiple interpolations,
# concatenation is the primary approach
let message = "Error: " + errorCode + " - " + errorMessage
```

### Important: No Built-in String Formatting

Unlike Go's `fmt.Sprintf()`, Dang does not have string formatting/interpolation. Use concatenation:

**Go:**
```go
url := fmt.Sprintf("https://example.com/%s/file", version)
refspec := fmt.Sprintf("%s:%s", sourceTag, destTag)
refSpec := fmt.Sprintf("refs/*%[1]s:refs/*%[1]s", strings.TrimPrefix(tag, "refs/"))
```

**Dang:**
```dang
let url = "https://example.com/" + version + "/file"
let refspec = sourceTag + ":" + destTag
let trimmedTag = tag.trimPrefix("refs/")
let refSpec = "refs/*" + trimmedTag + ":refs/*" + trimmedTag
```

---

## Dagger API Patterns

### Calling Dagger Functions

**In Go, you use `dag.FunctionName()`:**
```go
base := dag.Alpine(dagger.AlpineOpts{
    Branch: version,
    Packages: []string{"git", "go"},
}).Container()
```

**In Dang, functions are lowercase (following GraphQL conventions):**
```dang
let base = alpine(
  branch: version,
  packages: ["git", "go"]
).container
```

### Function Arguments: Positional vs Named

Dang supports positional, named, and mixed argument styles. Mixed style is common for readability:

**All named arguments:**
```dang
gitReleaser().release(
  sourceRepo: repo,
  dest: url,
  sourceTag: tag,
  destTag: version
)
```

**Mixed positional and named (common pattern):**
```dang
gitReleaser().release(
  repo,           # First arg positional
  url,            # Second arg positional
  tag,            # Third arg positional
  destTag: version,    # Remaining args named
  callback: cb,
  githubToken: token
)
```

**Why mix?** Required arguments that follow obvious order can be positional, while optional or less obvious arguments benefit from explicit names. This mirrors the GraphQL argument system.

### Common Dagger Functions

| Go | Dang | Notes |
|---|---|---|
| `dag.Alpine()` | `alpine()` | Returns Alpine module |
| `dag.HTTP()` | `http()` | Fetches HTTP resource |
| `dag.SetSecret()` | `setSecret()` | Creates secret |
| `.Container()` | `.container` | Property access |
| `.Ref()` | `.ref()` | Method call |
| `.Tree()` | `.tree()` | Method call |
| `.Plaintext(ctx)` | `.plaintext` | Property access (no ctx) |
| `.Sync(ctx)` | `.sync` | Property access (no ctx) |
| `.Stdout(ctx)` | `.stdout` | Property access (no ctx) |

### Method Chaining

Method chaining works identically in both languages:

```dang
let result = container.
  from("alpine:3.18").
  withExec(["apk", "add", "git"]).
  withWorkdir("/workspace").
  withDirectory(".", source).
  withExec(["git", "status"]).
  stdout
```

### Container Operations

```dang
# Building containers
let ctr = alpine(packages: ["git", "python3"]).container

# Adding files
ctr = ctr.withFile(
  "/usr/local/bin/script",
  http("https://example.com/script.sh"),
  permissions: 0o755  # Octal notation
)

# Environment variables
ctr = ctr.withEnvVariable("KEY", "value")

# Secret variables
ctr = ctr.withSecretVariable("TOKEN", mySecret)

# Executing commands
ctr = ctr.withExec(["command", "arg1", "arg2"])

# With options
ctr = ctr.withExec(
  ["go", "test", "./..."],
  experimentalPrivilegedNesting: true,
  expect: ReturnType.ANY  # Don't fail on non-zero exit
)
```

### Working with Secrets

```dang
pub myFunc(githubToken: Secret = null): Void {
  if githubToken != null {
    let tokenValue = githubToken.plaintext
    # Use tokenValue...

    # Or pass secret directly to container
    let ctr = container.withSecretVariable("GITHUB_TOKEN", githubToken)
  }
  null
}
```

### Git Operations

```dang
# Get a specific ref
let tree = git.ref(tagName).tree(depth: -1)

# Clone and work with repository
let ctr = container.
  withExec(["git", "clone", repoUrl, "."]).
  withExec(["git", "checkout", branch])
```

### Cache Volumes

```dang
let ctr = container.
  withMountedCache("/cache", cacheVolume("my-cache-key"))
```

---

## Error Handling

### The Big Difference

**Go has explicit error handling:**
```go
result, err := container.Stdout(ctx)
if err != nil {
    var execErr *dagger.ExecError
    if errors.As(err, &execErr) {
        if strings.Contains(execErr.Stderr, "specific error") {
            // Handle specific error
            return nil
        }
    }
    return err
}
```

**Dang has no explicit error handling:**
- Operations either succeed or fail
- Failed operations stop execution
- Use container exit codes and output checking for validation

### Handling Expected Failures

**Approach 1: Check before acting**

Instead of catching errors, check conditions first:

```dang
# Check if a ref exists before trying to fetch it
let lsRemoteOutput = container.
  withExec(["git", "ls-remote", url, refName]).
  stdout

if lsRemoteOutput != "" {
  # Ref exists, safe to fetch
}
```

**Approach 2: Use expect parameter**

```dang
# Allow command to fail without stopping execution
let result = container.
  withExec(["might-fail"], expect: ReturnType.ANY).
  stdout
```

**Approach 3: Fail explicitly**

```dang
if !isValid {
  # Cause explicit failure by running a command that exits 1
  container.
    withExec(["sh", "-c", "echo 'Error message' >&2; exit 1"]).
    sync
}
```

### Translating Error Return Patterns

**Pattern 1: Early return on error**

Go:
```go
func (m *Module) Process(ctx context.Context) error {
    result, err := step1(ctx)
    if err != nil {
        return err
    }
    err = step2(ctx, result)
    if err != nil {
        return err
    }
    return nil
}
```

Dang:
```dang
pub process(): Void {
  let result = step1()
  step2(result)
  null
}
```

**Pattern 2: Conditional error handling**

Go:
```go
result, err := operation(ctx)
if err != nil {
    if strings.Contains(err.Error(), "not found") {
        return nil  // This is OK
    }
    return err  // Other errors are real problems
}
```

Dang (refactor to check first):
```dang
# Check if resource exists
let exists = checkExists()
if exists {
  let result = operation()  # Only call if exists
}
```

---

## Known Limitations

### 1. No Base64 Encoding

**Issue:** Dang's stdlib doesn't include base64 encoding (as of this writing).

**Go code:**
```go
encodedPAT := base64.URLEncoding.EncodeToString([]byte("pat:" + token))
```

**Workarounds:**
- Use a container with `base64` command
- Pre-encode the value before passing to Dang
- Skip the encoding if the API accepts plain text (check API docs)

### 2. No Random/UUID Generation

**Issue:** No built-in random string generation for cache busting.

**Go code:**
```go
.WithEnvVariable("CACHEBUSTER", rand.Text())
```

**Workarounds:**
- Use a timestamp or version number instead
- Pass a cache-busting value as a parameter
- Accept that containers might be cached (often not an issue)

**Possible solution:**
```dang
pub myFunc(cacheBust: String = ""): Container! {
  let ctr = container
  if cacheBust != "" {
    ctr = ctr.withEnvVariable("CACHEBUSTER", cacheBust)
  }
  ctr
}
```

### 3. Limited String Formatting

**Issue:** No `printf`-style formatting.

**Solution:** Use string concatenation (see [String Operations](#string-operations))

### 4. No Regex Support (Yet)

**Issue:** No regex operations in stdlib currently.

**Workaround:** Use containers with `grep`, `sed`, or other tools.

---

## Common Patterns

### Pattern 1: Optional Parameters with Defaults

**Go:**
```go
func (m *Module) Build(
    // +optional
    platform string,
) {
    if platform == "" {
        platform = "linux/amd64"
    }
}
```

**Dang:**
```dang
pub build(platform: String! = "linux/amd64"): Container! {
  # platform is automatically "linux/amd64" if not provided
}
```

### Pattern 2: Building Lists Conditionally

**Go:**
```go
args := []string{"command", "subcommand"}
if flag {
    args = append(args, "--flag")
}
if param != "" {
    args = append(args, "--param", param)
}
```

**Dang:**
```dang
let args = ["command", "subcommand"]
if flag {
  args = args + ["--flag"]
}
if param != "" {
  args = args + ["--param", param]
}
```

### Pattern 3: Method Call Forwarding

**Go:**
```go
func (m *Module) DryRun(ctx context.Context, git *dagger.GitRepository) error {
    return m.Release(ctx, git, "", true)
}
```

**Dang:**
```dang
pub dryRun(git: GitRepository!): Void {
  release(git, "", true)
}
```

### Pattern 4: Mutable Container Building

**Go:**
```go
base := dag.Alpine().Container()
if condition {
    base = base.WithEnvVariable("KEY", "value")
}
base = base.WithExec([]string{"command"})
```

**Dang:**
```dang
let base = alpine().container
if condition {
  base = base.withEnvVariable("KEY", "value")
}
base = base.withExec(["command"])
```

### Pattern 5: Calling Other Dagger Modules

**Go:**
```go
// Available via dag.ModuleName()
result := dag.GitReleaser().Release(ctx, ...)
```

**Dang:**
```dang
# Available via lowercase function names
let result = gitReleaser.release(...)
```

---

## Porting Checklist

Use this checklist when porting a module:

- [ ] Remove Go package and import statements
- [ ] Convert type name and struct to `type TypeName { }`
- [ ] Convert struct fields:
  - [ ] Public fields (capitalized in Go) → `pub fieldName: Type!`
  - [ ] Private fields (marked `// +private` in Go) → `let fieldName: Type!`
- [ ] Convert constructor `New()` function parameters to type constructor parameters
- [ ] Convert Go annotations to Dang directives:
  - [ ] `// +defaultPath="/"` → `@defaultPath(path: "/")`
  - [ ] `// +ignore=[...]` → `@ignorePatterns(patterns: [...])`
  - [ ] `// +default="value"` → inline default `= "value"`
  - [ ] `// +optional` → nullable type (e.g., `String` instead of `String!`)
- [ ] Remove `ctx context.Context` from all function signatures
- [ ] Convert method receivers from `(m *Module)` to `pub methodName`
- [ ] Convert optional parameter comments to inline defaults
- [ ] Change `error` returns to `Void` (and add `null` at end)
- [ ] Change `(T, error)` returns to just `T!` or `T`
- [ ] Convert `dag.FunctionName` to `functionName`
- [ ] Convert `.Method(ctx)` to `.method` (remove ctx, lowercase)
- [ ] Convert `fmt.Sprintf()` to string concatenation
- [ ] Convert `strings.TrimPrefix()` to `.trimPrefix()`
- [ ] Convert `strings.Contains()` to `.contains()`
- [ ] Convert enum constant access to dot notation (e.g., `CheckCompleted` → `Status.COMPLETED`)
- [ ] Handle error checking differently (see [Error Handling](#error-handling))
- [ ] Test the module with `dagger call`

---

## Testing Your Port

### Basic Test

```bash
dagger call --help
```

Should show all your public functions with their parameters.

### Function Test

```bash
dagger call my-function --param value
```

### Debugging

Enable debug output:
```bash
dagger call my-function --debug
```

---

## Additional Resources

- [Dang Repository](https://github.com/vito/dang)
- [Dang Language Tests](https://github.com/vito/dang/tree/main/tests) - Comprehensive examples
- [Dagger Documentation](https://docs.dagger.io/)

---

## Version History

- **v1.0** (2025-11-07): Initial version based on git-releaser porting experience
