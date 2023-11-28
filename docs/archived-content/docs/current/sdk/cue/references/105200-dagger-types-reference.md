---
slug: /sdk/cue/105200/dagger-types-reference
displayed_sidebar: 'current'
---

# Dagger CUE SDK Types Reference

Dagger CUE SDK Types are primitives that hold internal references to values stored in the Dagger Engine. They extend the CUE type system and can be used in [Actions](../core-concepts/934917-action.md). Their definitions can be imported from the `dagger.io/dagger` package.

The following types are available:

| Definition     | File                                                                                        | Description                                           |
| :------------- | :------------------------------------------------------------------------------------------ | :---------------------------------------------------- |
| `#FS`          | [types.cue](https://github.com/dagger/dagger/blob/v0.2.7/pkg/dagger.io/dagger/types.cue)    | Reference to a filesystem tree                        |
| `#Secret`      | [types.cue](https://github.com/dagger/dagger/blob/v0.2.7/pkg/dagger.io/dagger/types.cue)    | Secure reference to an external secret                |
| `#Socket`      | [types.cue](https://github.com/dagger/dagger/blob/v0.2.7/pkg/dagger.io/dagger/types.cue)    | Reference to a network socket: unix or npipe          |

And there's a special instance of a Type:

| Definition     | File                                                                                        | Type  | Description                                   |
| :------------- | :------------------------------------------------------------------------------------------ | : --- | :-------------------------------------------- |
| `#Scratch`     | [values.cue](https://github.com/dagger/dagger/blob/v0.2.7/pkg/dagger.io/dagger/values.cue)  | `#FS` | An empty filesystem tree                      |

## Data structures

There's also some data structures that are tightly coupled to [core actions](./565505-core-actions-reference.md). Their definitions are in the `dagger.io/dagger/core` package.

### Related to mounts

| Definition     | File                                                                                             | Description                                           |
| :------------- | :----------------------------------------------------------------------------------------------- | :---------------------------------------------------- |
| `#Mount`       | [core/exec.cue](https://github.com/dagger/dagger/blob/v0.2.7/pkg/dagger.io/dagger/core/exec.cue) | Transient filesystem mount                            |
| `#CacheDir`    | [core/exec.cue](https://github.com/dagger/dagger/blob/v0.2.7/pkg/dagger.io/dagger/core/exec.cue) | A (best effort) persistent cache dir                  |
| `#TempDir`     | [core/exec.cue](https://github.com/dagger/dagger/blob/v0.2.7/pkg/dagger.io/dagger/core/exec.cue) | A temporary directory for command execution           |

### Related to container images

| Definition     | File                                                                                             | Description                                           |
| :------------- | :----------------------------------------------------------------------------------------------- | :---------------------------------------------------- |
| `#ImageConfig` | [core/image.cue](https://github.com/dagger/dagger/blob/v0.2.7/pkg/dagger.io/dagger/core/image.cue)    | Container image config                                |
| `#HealthCheck` | [core/image.cue](https://github.com/dagger/dagger/blob/v0.2.7/pkg/dagger.io/dagger/core/image.cue)    | Container health check                                |
