---
slug: /1213/api
displayed_sidebar: europa
---

# Dagger CUE API (0.2+)

As of Dagger 0.2 (codename Europa), the Dagger CUE API can be imported via `dagger.io/dagger`

The Dagger CUE API is the set of CUE packages released alongside the Dagger engine.

Developers of other Dagger packages are expected to build on top of these core packages.

### Plan definition

`#Plan` defines the structure of a Dagger plan - a complete configuration executable by `dagger do`

| Definition | File                                                                                   | Description                                          |
| :--        | :--                                                                                    | :--                                                  |
| `#Plan`    | [plan.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/plan.cue) | A special kind of program which `dagger` can execute |

### Core types

Dagger extends the CUE type system with the following core types:

| Definition     | File                                                                                     | Description                                 |
| :--            | :--                                                                                      | :--                                         |
| `#Address`     | [types.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/types.cue) | Network service address                     |
| `#CacheDir`    | [exec.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/exec.cue)   | A (best effort) persistent cache dir        |
| `#FS`          | [types.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/types.cue) | Reference to a filesystem tree              |
| `#HealthCheck` | [image.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/image.cue) | Container health check                      |
| `#ImageConfig` | [image.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/image.cue) | Container image config                      |
| `#Mount`       | [exec.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/exec.cue)   | Transient filesystem mount                  |
| `#Ref`         | [image.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/image.cue) | An address for a remote container image     |
| `#Scratch`     | [types.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/types.cue) | An empty directory                          |
| `#Secret`      | [types.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/types.cue) | Secure reference to an external secret      |
| `#Service`     | [types.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/types.cue) | Reference to network service endpoints      |
| `#TempDir`     | [exec.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/exec.cue)   | A temporary directory for command execution |

### Core actions

Dagger works by executing actions in a certain order and passing data between actions in a certain layout.

Developers can combine actions into higher-level actions, which in turn can be combined into even higher-level actions,
and so on at arbitrary levels of depth. At the bottom of this abstraction stack are *core actions*: these
are implemented by Dagger itself, and are always available.

The following core actions are available:

| Definition      | File                                                                                         | Description                                              |
| :--             | :--                                                                                          | :--                                                      |
| `#Copy`         | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/fs.cue)           | Copy files between two filesystem trees                  |
| `#DecodeSecret` | [secrets.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/secrets.cue) | Decode a secret without leaking its contents             |
| `#Dockerfile`   | [image.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/image.cue)     | Build a container image using a Dockerfile               |
| `#Exec`         | [exec.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/exec.cue)       | Execute a command in a docker-compatible container       |
| `#GitPull`      | [git.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/git.cue)         | Download a repository from a remote git server           |
| `#GitPush`      | [git.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/git.cue)         | Upload a repository to a remote git server               |
| `#HTTPFetch`    | [http.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/http.cue)       | Download an archive from an HTTP server                  |
| `#Mkdir`        | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/fs.cue)           | Create a directory in a filesystem tree                  |
| `#NewSecret`    | [secrets.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/secrets.cue) | Dynamically create a new secret                          |
| `#Pull`         | [image.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/image.cue)     | Download an image from a docker registry                 |
| `#Push`         | [image.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/image.cue)     | Upload an image to a docker registry                     |
| `#ReadFile`     | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/fs.cue)           | Read a file from a filesystem tree                       |
| `#Scratch`      | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/fs.cue)           | Create an empty filesystem tree                          |
| `#Set`          | [image.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/image.cue)     | Modify a docker image                                    |
| `#Source`       | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/fs.cue)           | Access the source for the current CUE package            |
| `#Subdir`       | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/fs.cue)           | Read a subdirectory from a filesystem tree               |
| `#TrimSecret`   | [secrets.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/secrets.cue) | Trim leading and trailing space characters from a secret |
| `#WriteFile`    | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.0/pkg/dagger.io/dagger/fs.cue)           | Write a file to a filesystem tree                        |
