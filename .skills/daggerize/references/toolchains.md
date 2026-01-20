# Toolchains (Dagger)

## When to use a toolchain
- You want project-local commands like `dagger call <toolchain> build`.
- You do not want the repo itself to be a reusable Dagger module for other modules.
- You want to keep automation code in `toolchains/<name>`.

## Typical structure
```
<repo>/
  dagger.json
  toolchains/
    <name>/
      dagger.json
      src/
```

## Root dagger.json (register toolchain)
```
{
  "name": "repo-name",
  "toolchains": [
    { "name": "server", "source": "./toolchains/server" }
  ]
}
```

## Initialize a toolchain (TypeScript example)
```
mkdir -p toolchains/server
cd toolchains/server
# or run from repo root with a path
# dagger init --sdk=typescript toolchains/server
```

## Invoke toolchain functions
```
# human-friendly
 dagger call server build

# LLM/CI logs
 dagger --progress=plain call server build
```

## Tip
Toolchain functions should accept `Directory` inputs with `defaultPath` to reference local source trees.
