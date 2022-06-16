---
slug: /1226/coding-style
displayed_sidebar: "0.2"
---

# Package Coding Style

Please follow these guidelines when contributing CUE packages to keep consistency,
improve clarity and avoid issues.

## Public names

Choose `PascalCase` for "public" definitions (importable outside of package).

```cue
// good
#WriteFile: {}

// bad
#writeFile: {}
#write_file: {}
```

Choose `camelCase` for "public" fields, instead of `snake_case` or `"kebab-case"`.

```cue
// good
sshKey: ...

// bad
ssh_key: ...
"ssh-key": ...
```

## Private names

Choose `_#camelCase` for private definitions that should only be used in current package.

```cue
// good
_#mergeStructs: { ... }

// bad, starts with lower case but not private
#mergeStructs: { ... }

// bad, starts with upper case but private
_#MergeStructs: { ... }
```

Choose `_camelCase` for private fields (implementation details).

```cue
#Copy: {
    // good, indicates internal implementation
    _copy: core.#Copy

    // bad, indicates it can be used directly
    copy: core.#Copy
}
```

## Definitions for _schemas_, fields for concrete _implementations_

```cue
// good, defines a schema
#Copy: {
    // good, implements a schema
    input: #Image
    _copy: core.#Copy & { ... }
}

// bad, not a definition
Copy: {
    // bad, not using a visible or hidden field (according to intent)
    #input: #Image
    _#copy: core.#Copy & { ... }
}

// ok for mixin
Mixin: { ... }
copy: #Copy & Mixin & { ... }
```

## All public definitions and fields must be documented

Documenting other fields is optional. The main idea is that public fields should be documented for someone
reading Dagger’s documentation in the browser. Everything else is for someone looking at the code.

```cue
// Copy files into an image
#Copy: {
    // Image to receive copied files
    input: #Image

    // Files to copy
    contents: dagger.#FS

    _copy: ...

    // Resulting image with added files
    output: ...
}
```

## One definition per file

Split action definitions into their own file as much as possible (unless very tightly related).

```cue
// copy.cue
#Copy: ...

// push.cue
#Push: ...

// exec.cue
#Exec: ...
#Mount: ...
#CacheDir: ...
```

## Use verbs for action names, nouns otherwise

Action definition names should be verbs (e.g., `#Build`, `#Run`, `#Push`).
Other types of definitions should be nouns as much as possible (e.g., `#Image`, `#Secret`, `#Socket`).

## No field abbreviations

CUE fields are more explicit and intuitive without abbreviations (e.g, `target` or
`destination` instead of `dest`).

## Avoid interpolation

There's no need to interpolate a variable if it's already
a string.

```cue
let greet = "hello world"

// bad
args: ["echo", "\(greet)"]
read: contents: "\(greet)"

// good
args: ["echo", greet]
read: contents: greet
```

Even for field names there's a better way:

```cue
// bad
client: filesystem: "\(path)": read: contents: string
files: "\(path)": output.contents

// good
client: filesystem: (path): read: contents: string
files: (path): output.contents
```

It's ok if you need to convert something into a string.

```cue
exit: int

env: [string]: string
env: EXIT: "\(exit)"
```

Interpolating a really short script is actually harmless,
but not if it discourages from splitting the script into
its own file.

```cue
// bad
let name = "world"
run: bash.#Run & {
    script: contents: "echo hello \(world)"
}

// good
run: bash.#Run & {
    env: NAME: name
    script: contents: "echo hello $NAME"
}
```

## Don’t inline scripts

Avoid inlining scripts (e.g., _sh_, _py_, etc). Instead, put them in their own files
with proper extension, and use `core.#Source` (see [When to use `core.#Source`](../guides/actions/1240-core-source.md)) to import into CUE. This allows linting
and avoids some limitations (script size, escaping).

Some exceptions may apply (e.g., the script is really short or interpolation is
actually necessary).

```cue
// bad
run: bash.#Run & {
    script: contents: """
        ...
        ...
        ...
        """
}

// good
src: core.#Source & {
    path: "./src"
}
run: bash.#Run & {
    script: {
        directory: src.output
        filename: "setup.sh"
    }
}

// ok
run: bash.#Run & {
    script: contents: "echo hello world"
}
```

## Avoid raw strings

Don’t use `#"""` unless you actually need it. Most of the time you won't.
The regular `"""` is simpler to read and less scary for beginners.

<!-- TODO: Add example where it actually helps or is ok. -->

```cue
// bad
run: python.#Run & {
    script: contents: #"""
        print("Hello World!")
        """#
}

// good
run: python.#Run & {
    script: contents: """
        print("Hello World!")
        """
}

// bad
run: python.#Run & {
    script: contents: #"print("Hello World!")"#
}

// good
run: python.#Run & {
    script: contents: "print('Hello World')"
}

// good
run: python.#Run & {
    script: contents: 'print("Hello World")'
}
```

## Favor disjunctions over _if_ conditions

```cue
// bad
type: string
if type == "fs" {
    contents: dagger.#FS
    ro?: true | *false
}
if type == "cache" {
    contents: dagger.#Secret
    mask: int
}

// good
{
    contents: dagger.#FS
    ro?: true | *false
} | {
    contents: dagger.#Secret
    mask: int
}
```

## Favor templates over _for_ loops

```cue
// bad
files: ["stdout.log", "stderr.log"]
export: {
    for path in files {
        (path): #Read & {
            "path": path
        }
    }
}

// good
export: [path=string]: #Read & {
    "path": path
}
export: {
    "stdout.log": _
    "stderr.log": _
}
```

## Scope conditions and loops as locally as possible

```cue
// bad
if host != _|_ {
    env: HOST: host
}

// good
env: {
    if host != _|_ {
        HOST: host
    }
}

// bad
for path, output in _files {
    files: (path): output.contents
}

// good
files: {
    for path, output in _files {
        (path): output.contents
    }
}
```

## Use _top_ to match anything

From [CUE](https://cuelang.org/docs/references/spec/#values-1):

> At the top of the lattice is the single ancestor of all values, called _top_, denoted `_` in CUE. Every value is an instance of top.

There's a recurring theme when you have a template and need to create instances from it:

```cue
// bad
files: [path=string]: {
    "path": path
    ...
}

// bad
files: "/output": {}

// good
files: "/output": _

```

And also when you need to reference something that's being added implicitly.

```cue
actions: build: go.#Run & {
    // these values are being added implicitly to the plan
    // but we need something to reference
    os: client.platform.os
    arch: client.platform.arch
    ...
}

// bad, no need to redefine these fields
client: platform: {
    os: string
    arch: string
}

// good, we only need the `client` field to exist
client: _


// ok if it improves understanding
client: platform: _
```

## Format all the CUE code

It's important to lint all the CUE code so it stays consistent across all packages.

Thanksfully, CUE makes it very easy. [First, make sure you have the CUE binary installed on your machine](https://cuelang.org/docs/install/). Then, simply run the following command from the package directory:

```shell
cue fmt -s
```
