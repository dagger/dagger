---
slug: /1232/chain-actions
displayed_sidebar: '0.2'
---

# How can I chain actions together ?

Dependencies are materialized at runtime, when your Cue files are parsed and the corresponding DAG gets generated:

```cue
// Prerequisite action that runs when `test` is being called
_dockerCLI: alpine.#Build & {
    packages: bash: _
}

// Main action
foo: bash.#Run & {
    input: _dockerCLI.output // <== CHAINING of action happening here
    script: contents: #"""
        echo "hello"
        """#
}
```

On above example, `_dockerCLI` gets executed first, as the corresponding DAG shows that it is required for the action `foo` to be processed.

This is the `input-output` model: `foo`'s input is `_dockerCLI`'s output.

We currently do not support explicit dependencies at the moment (one of our top priority). But, if you need one, here is how you can hack your way around it:

```cue
foo: bash.#Run & {
    input: _dockerCLI.output
    script: contents: #"""
        echo "hello"
        """#
}

// Main action
bar: bash.#Run & {
    input: _dockerCLI.output
    script: contents: #"""
        echo "hello"
        """#
    env: HACK: "\(test.success)" // <== HACK: CHAINING of action happening here
}
```

`foo` and `bar` are similar actions. If you do not want to rely on the `input-output` model but still want to force a dependency between them. The easiest way is to create an environment variable that relies on the other action's success
