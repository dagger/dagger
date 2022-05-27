---
slug: /1240/core-source
displayed_sidebar: "0.2"
---

# When to use `core.#Source`?

The [#Source core action](../references/1222-core-actions-reference.md#core-actions-related-to-filesystem-trees) seems to do the same as `client: filesystem: ...: read: contents: dagger.#FS`, although there's important differences.

## Purpose

The purpose of `core.#Source` is to **enable including files with reusable packages** in a secure way.

The most common example of this is writing scripts in their own file extensions, and importing in an action instead of inlining the contents of the script (see [Don't inline scripts](../guides/1226-coding-style.md#dont-inline-scripts)).

For example, here's how the [netlify package](https://github.com/dagger/dagger/tree/main/pkg/universe.dagger.io/netlify) imports the `deploy.sh` script:

```cue title="pkg/universe.dagger.io/netlify/netlify.cue"
// Deploy a site to Netlify
#Deploy: {
    ...
    container: bash.#Run & {
        ...
        script: {
            _load: core.#Source & {
                path: "."
                include: ["*.sh"]
            }
            directory: _load.output
            filename:  "deploy.sh"
        }
        ...
    }
    ...
}
```

You can include all sorts of files this way, with your [reusable packages](../guides/1239-making-reusable-package.md).

## Path is relative to the file where it's defined

Notice in the example above that the path to `deploy.sh` is relative to the file where `core.#Source` is being used (`netfily/netlify.cue`), unlike in [Client API](../core-concepts/1203-client.md#accessing-the-file-system) where paths are relative to where the `dagger` client is being run (current working directory).

This means that no matter where you run `dagger` from, the path in `core.#Source` will mean the same thing. You can use it in your plan directly if this is advantageous. A common example of that is when making integration test plans, for adding test data that is scoped to each plan.

## Scoped to the directory where it's defined

For security reasons, you can't access files outside of this directory. You need to use `client: filesystem` for that, which can use any path, even absolute ones.

Remember, the purpose of `core.#Source` is to enable including files in packages in a secure way. Any other access to files in the host where the `dagger` client is being run, must be explicitly declared with the [Client API](../core-concepts/1203-client.md).

```cue title="~/projects/test/dagger.cue"
ssh: core.#Source & {
    // This won't work
    // You can only reference files in ~/projects/test or deeper
    path: "~/.ssh"
}
foobar: core.#Source & {
    // Also won't work
    path: "../foobar"
}
```
