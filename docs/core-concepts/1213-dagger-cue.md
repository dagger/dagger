---
slug: /1213/api
displayed_sidebar: '0.2'
---

# Dagger CUE API

As of Dagger 0.2, the Dagger CUE API can be imported via `dagger.io/dagger` & `dagger.io/dagger/core`

The Dagger CUE API is the set of CUE packages released alongside the Dagger engine.

Developers of other Dagger packages are expected to build on top of these core packages.

### Plan definition

`#Plan` defines the structure of a Dagger plan - a complete configuration executable by `dagger do`

| Definition | File                                                                                   | Description                                          |
| :--------- | :------------------------------------------------------------------------------------- | :--------------------------------------------------- |
| `#Plan`    | [plan.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/plan.cue) | A special kind of program which `dagger` can execute |

### Core types

Dagger extends the CUE type system with the following core types:

| Definition     | File                                                                                          | Description                                           |
| :------------- | :------------------------------------------------------------------------------------------   | :---------------------------------------------------- |
| `#Address`     | [types.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/types.cue)      | Network service address                               |
| `#CacheDir`    | [exec.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/exec.cue)   | A (best effort) persistent cache dir                  |
| `#FS`          | [types.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/types.cue)      | Reference to a filesystem tree                        |
| `#HealthCheck` | [image.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/image.cue) | Container health check                                |
| `#ImageConfig` | [image.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/image.cue) | Container image config                                |
| `#Mount`       | [exec.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/exec.cue)   | Transient filesystem mount                            |
| `#Ref`         | [image.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/image.cue) | An address for a remote container image               |
| `#Scratch`     | [types.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/types.cue)      | An empty directory                                    |
| `#Secret`      | [types.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/types.cue)      | Secure reference to an external secret                |
| `#Socket`      | [types.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/types.cue)      | Reference to a network socket: unix, tcp/udp or npipe |
| `#TempDir`     | [exec.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/exec.cue)   | A temporary directory for command execution           |

### Core actions

Dagger works by executing actions in a certain order and passing data between actions in a certain layout.

Developers can combine actions into higher-level actions, which in turn can be combined into even higher-level actions,
and so on at arbitrary levels of depth. At the bottom of this abstraction stack are _core actions_: these
are implemented by Dagger itself, and are always available.

The following core actions are available:

| Definition      | File                                                                                              | Description                                                           |
| :-------------- | :------------------------------------------------------------------------------------------------ | :-------------------------------------------------------------------- |
| `#Copy`         | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/fs.cue)           | Copy files between two filesystem trees                               |
| `#DecodeSecret` | [secrets.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/secrets.cue) | Decode a secret without leaking its contents                          |
| `#Diff`         | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/fs.cue)           | Extract the difference between two filesystems as its own file system |
| `#Dockerfile`   | [image.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/image.cue)     | Build a container image using a Dockerfile                            |
| `#Exec`         | [exec.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/exec.cue)       | Execute a command in a docker-compatible container                    |
| `#Export`       | [image.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/image.cue)     | Export an image as a tar archive                                      |
| `#GitPull`      | [git.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/git.cue)         | Download a repository from a remote git server                        |
| `#HTTPFetch`    | [http.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/http.cue)       | Get a file from an HTTP server                                        |
| `#Merge`        | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/fs.cue)           | Merge multiple filesystem trees                                       |
| `#Mkdir`        | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/fs.cue)           | Create a directory in a filesystem tree                               |
| `#NewSecret`    | [secrets.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/secrets.cue) | Create a new a secret from a filesystem tree                          |
| `#Pull`         | [image.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/image.cue)     | Download an image from a docker registry                              |
| `#Push`         | [image.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/image.cue)     | Upload an image to a docker registry                                  |
| `#ReadFile`     | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/fs.cue)           | Read the contents of a UTF-8 encoded file from a filesystem tree      |
| `#Scratch`      | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/fs.cue)           | Create an empty filesystem tree                                       |
| `#Set`          | [image.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/image.cue)     | Modify a docker image                                                 |
| `#Source`       | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/fs.cue)           | Access the source for the current CUE package                         |
| `#Subdir`       | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/fs.cue)           | Read a subdirectory from a filesystem tree                            |
| `#TrimSecret`   | [secrets.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/secrets.cue) | Trim leading and trailing space characters from a secret              |
| `#WriteFile`    | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.11/pkg/dagger.io/dagger/core/fs.cue)           | Write a file to a filesystem tree                                     |
