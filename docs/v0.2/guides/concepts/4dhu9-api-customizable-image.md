---
slug: /4dhu9/api-customizable-image
displayed_sidebar: '0.2'
---

# Packages with customizable images

You should move away from having a default image inside main actions: for example, actions such as [go](https://github.com/dagger/dagger/blob/d45b946f024c63bcb89d22bd843a011f18d64b69/pkg/universe.dagger.io/go/build.cue#L9-L83) are not as flexible and efficient as [golangci](https://github.com/dagger/dagger/blob/d45b946f024c63bcb89d22bd843a011f18d64b69/pkg/universe.dagger.io/alpha/go/golangci/lint.cue#L17-L48).

Two reasons:

1. Default values cannot be directly appended to a [composite action](/1221/action/#composite-actions)
2. Even if you override a default image with a custom one, the default will still be [evaluated and executed](https://github.com/dagger/dagger/blob/d45b946f024c63bcb89d22bd843a011f18d64b69/pkg/universe.dagger.io/go/build.cue#L34), which is wasteful

## Three main action patterns

1. There should be a version without a default image
2. There should be an image definition (e.g. `bash.#Image`)
3. There should be a variation of the main action with the `#Image` already set up

For the purpose of the guide, let's create a simple, customizable `Python` action.

### 1. Main action without a default image

Each main action should be defined without a default image: users will be required to provide it during usage.

```cue file=../../tests/guides/customizable-image/run_base.cue

```

The above `#RunBase` action is a composite action: we inherit the fields of [docker.#Run](https://github.com/dagger/dagger/blob/main/pkg/universe.dagger.io/docker/run.cue#L11-L70) and only require the end-user to specify `directory`, `filename` and `args`.

Using these inputs, we prefill some of the `docker.#Run`'s field to automatically run the script with the inputs.

:::note
As it is a composition, the `#RunBase` action inherits the `input` field from the `docker.#Run` action.

However, we never fill it. This is intended, as we want to enforce a design pattern around main actions with customizable images.
:::

### 2. The image definition

There should be an image definition (e.g. `python.#Image`) that can easily be used with the `#RunBase` action, for the most common cases.

```cue file=../../tests/guides/customizable-image/image_simple.cue

```

But if you're adding a lot of configuration just to tweak the image source:

```cue file=../../tests/guides/customizable-image/image_configurable.cue

```

Then users should probably just use another custom image instead:

```cue file=../../tests/guides/customizable-image/image_pip.cue

```

You can see that the first `python.#Image` is pretty basic. It doesn't contain any configuration, and is the expected, minimal image that creators of main actions shall include in their packages.

:::note
It is not the responsibility of the action's creator to make the image as modular as possible: a simple version covering 90% of the most common use-cases is the only requirement
:::

### 3. Variation with the image already set up

Each point builds upon the last, from flexibility to simplified usage.

```cue file=../../tests/guides/customizable-image/run.cue

```

This variation implements a ready-to-be-used action where no image is required.
It will not fit all the use-cases, but will be users of this action gain time.
