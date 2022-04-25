---
slug: /1226/handling-outputs
displayed_sidebar: 0.2
---

# Handling action outputs

It's most likely that upon executing a Dagger action, you'll find in the situation of having to parse some outputs. In Dagger 0.1 (pre-Europa), inputs and outputs had to be specifically defined; starting Dagger 0.2 (Europa) we introduced a brand new API which removed this capability temporarily until we come up with the [right UX](https://github.com/dagger/dagger/issues/1351) to bring this back. Until the API is ready, you can follow this guide which shows different **temporary** alternatives of parsing dagger outputs.

## Writing outputs to the filesystem

As showcased in the [interacting with the client](/1203/client) docs, Dagger already has the ability to write into the client filesystem through the `client` API. Using this capability we can then output any value into our local filesystem for further automation.

Here's a simple example of a plain text output:

```cue file=../tests/core-concepts/client/plans/output_simple.cue.fragment
```

After performing `dagger do test`, a new file named `output.txt` will be present in the current working directory with the output of the `bash.#Run` action.

## Marshalling multiple outputs

The above example works well for simple outputs, but most of the time some actions like [Netlify's](https://github.com/dagger/dagger/blob/main/pkg/universe.dagger.io/netlify/netlify.cue) can output mutiple values. In this case, Netlify's action is returning the deployment `url`, `deployUrl` and `logsUrl`. On this case, we can leverage on CUE's [default integrations](https://cuelang.org/docs/integrations/) and marshal all the values into a single `json` or `yaml` file.

Here's an example on how to marshal multiple output values:

```cue file=../tests/core-concepts/client/plans/output_marshal.cue.fragment
```
