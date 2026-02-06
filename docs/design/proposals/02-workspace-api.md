# Part 2: Workspace API

*Builds on [Part 1: Module vs. Workspace](https://gist.github.com/shykes/e4778dc5ec17c9a8bbd3120f5c21ce73)*

## Table of Contents

- [Problem](#problem)
- [Solution](#solution)
- [The Workspace Type](#the-workspace-type)
  - [Injection](#injection)
  - [Cannot Be Stored](#cannot-be-stored)
  - [Caching](#caching)
- [Examples](#examples)
  - [Go Toolchain](#go-toolchain)
  - [Node Toolchain](#node-toolchain)
- [Comparison with +defaultPath](#comparison-with-defaultpath)
- [Status](#status)

## Problem

Currently, modules use `+defaultPath` and `+ignore` to access files from the context they're called in:

```go
func New(
    // +defaultPath="."
    // +ignore=["*", "!**/*.go", "!go.mod", "!go.sum"]
    source *dagger.Directory,
) *Go {
    // ...
}
```

This has limitations:

1. **Implicit contract** - `+defaultPath` doesn't signal "I'm a workspace module." It just says "give me a directory if the user doesn't provide one." There's no way for a module to declare that it's designed to be installed in a workspace and extend its capabilities.

2. **No dynamic discovery** - While `+defaultPath` combined with `+ignore` allows some globbing, truly dynamic path discovery is not possible. For example, a toolchain that needs to parse `package.json` to find monorepo package paths, or read a config file to determine which directories to process, cannot express this with static pragma arguments.

3. **Breaks programming model** - Everything else in a Dagger module follows familiar patterns: objects, functions, arguments, return values. But this critical interface is expressed outside the code itself - in Go it's pragmas with JSON-encoded strings, in other SDKs it's decorators that conflict with native default value syntax. A fundamental capability expressed through an awkward escape hatch.

## Solution

Replace `+defaultPath` with an explicit **Workspace** type that modules can receive as a constructor argument.

## The Workspace Type

*Extended in [Part 3: Artifacts](https://gist.github.com/shykes/aa852c54cf25c4da622f64189924de99#workspace-api)*

```graphql
"""
Provides explicit access to the context a module is installed in.
"""
type Workspace {
  """
  Returns a Directory from the workspace.
  Path is relative to workspace root. Use "." for the root.
  """
  directory(
    path: String!
    """Glob patterns to include (e.g. ["**/*.go", "go.mod"])"""
    include: [String!]
    """Glob patterns to exclude (e.g. ["**/testdata"])"""
    exclude: [String!]
  ): Directory!

  """
  Returns a File from the workspace.
  Path is relative to workspace root.
  """
  file(path: String!): File!

  """
  Search for a file or directory by walking up the tree from the given path.
  Returns the relative path if found, null otherwise.
  """
  findUp(
    """Name of the file or directory to search for (e.g. "go.mod", ".git")"""
    name: String!
    """Path to start searching from (defaults to workspace root)"""
    from: String
  ): String

  """
  Search for content matching a regular expression or literal string.
  Uses Rust regex syntax.
  """
  search(
    """The pattern to match"""
    pattern: String!
    """Directory or file paths to search"""
    paths: [String!]
    """Glob patterns to filter files (e.g. ["*.go"])"""
    globs: [String!]
    """Interpret pattern as literal string instead of regex"""
    literal: Boolean
    """Enable searching across multiple lines"""
    multiline: Boolean
    """Enable case-insensitive matching"""
    insensitive: Boolean
    """Only return matching file paths, not lines and content"""
    filesOnly: Boolean
    """Limit the number of results"""
    limit: Int
  ): [SearchResult!]!
}
```

### Injection

Any function can declare a `Workspace` argument:

```go
func New(ws dagger.Workspace) *Go {
    return &Go{
        Source: ws.Directory("."),
    }
}
```

**Schema registration:** Workspace arguments are always registered as optional, regardless of how they're declared in code.

**When set explicitly:** The caller-provided Workspace is used normally.

**When not set:** The engine injects the current workspace from context. This never fails - there is always a workspace in context, but it may be empty (rooted in an empty directory).

An empty workspace is injected when:
- The function is called from another module (module-to-module calls)
- The CLI cannot determine a workspace root (no `.git` found, no `.dagger/config.toml`, no `--workspace-root` flag, no `dagger.json` for backwards compatibility)
- The CLI is invoked with `--no-workspace`

This means modules can always safely declare a Workspace argument. If they're called without workspace context, they simply receive an empty workspace - no files, no configuration. The module can check for this and behave accordingly.

### Cannot Be Stored

A Workspace cannot be stored as a field on a module object:

```go
type Go struct {
    Workspace dagger.Workspace  // ERROR: Workspace cannot be stored
    Source    *dagger.Directory // OK: Directory can be stored
}
```

Functions that need workspace access declare it as an argument. This keeps workspace dependencies visible in function signatures.

*Open question: Is this constraint still necessary with the new injection rules? The main effect is forcing explicit declaration rather than hidden state.*

### Caching

Workspace arguments affect function caching differently depending on the workspace source:

**Git remote workspace:** The cache key is the git tree hash. If any file in the repository changes, the function is invalidated and re-runs. This is coarse-grained but correct - the engine can compute the hash before calling the function.

**Local directory workspace:** The function always runs. Local directories cannot be content-hashed until their contents are uploaded to the engine, but which files to upload is determined dynamically by the function's `directory()` and `file()` calls. Since the engine can't know the cache key without running the function, it must always run it.

This means local development always re-runs functions with Workspace arguments, while CI (typically using git remotes) benefits from caching.

## Examples

### Go Toolchain

```go
func New(ws dagger.Workspace) *Go {
    // Find all Go modules in the workspace
    goModFiles := ws.Directory(".").Glob("**/go.mod")

    var modules []*GoModule
    for _, path := range goModFiles {
        dir := filepath.Dir(path)
        modules = append(modules, &GoModule{
            Source: ws.Directory(dir),
            Path:   dir,
        })
    }

    return &Go{Modules: modules}
}

type Go struct {
    Modules []*GoModule
}

type GoModule struct {
    Source *dagger.Directory
    Path   string
}
```

### Node Toolchain

```go
func New(ws dagger.Workspace) *Node {
    // Read package.json to understand project structure
    pkg := ws.File("package.json")

    // Find all packages in monorepo
    packages := ws.Directory(".").Glob("packages/*/package.json")

    return &Node{
        Root:     ws.Directory("."),
        Packages: parsePackages(packages),
    }
}
```

## Comparison with +defaultPath

| Aspect | `+defaultPath` | `Workspace` |
|--------|----------------|-------------|
| Explicitness | Hidden in pragma | Visible in signature |
| Context awareness | None | Knows it's in a workspace |
| Path discovery | Static patterns only | Dynamic (read configs, glob, etc.) |
| Works outside workspace | Yes (from cwd) | Yes (empty workspace injected) |

Workspace is always injected (possibly empty), so modules work in any context.

## Status

POC implementation exists on `toolchains-v2` branch.

---

- Previous: [Part 1: Module vs. Workspace](https://gist.github.com/shykes/e4778dc5ec17c9a8bbd3120f5c21ce73)
- Next: [Part 3: Artifacts](https://gist.github.com/shykes/aa852c54cf25c4da622f64189924de99)
