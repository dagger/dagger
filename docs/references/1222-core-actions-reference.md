---
slug: /1222/core-actions-reference
displayed_sidebar: europa
---

# Core Actions Reference

Core Actions are primitives implemented by the Dagger Engine itself. They can be combined into higher-level composite actions. Their definitions can be imported in the `dagger.io/dagger/core` package.

For more information about Dagger Actions, see [Dagger Actions](../core-concepts/1221-action).

The following core actions are available:

## Core Actions related to filesystem trees

| Definition      | File                                                                                              | Description                                                           |
| :--             | :--                                                                                               | :--                                                                   |
| `#Copy`         | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/fs.cue)           | Copy files between two filesystem trees                               |
| `#Diff`         | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/fs.cue)           | Extract the difference between two filesystems as its own file system |
| `#Merge`        | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/fs.cue)           | Merge multiple filesystem trees                                       |
| `#Mkdir`        | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/fs.cue)           | Create a directory in a filesystem tree                               |
| `#ReadFile`     | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/fs.cue)           | Read a file from a filesystem tree                                    |
| `#Scratch`      | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/fs.cue)           | Create an empty filesystem tree                                       |
| `#Source`       | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/fs.cue)           | Access the source for the current CUE package                         |
| `#Subdir`       | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/fs.cue)           | Read a subdirectory from a filesystem tree                            |
| `#WriteFile`    | [fs.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/fs.cue)           | Write a file to a filesystem tree                                     |

## Core Actions related to secrets

| Definition      | File                                                                                              | Description                                                           |
| :--             | :--                                                                                               | :--                                                                   |
| `#DecodeSecret` | [secrets.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/secrets.cue) | Decode a secret without leaking its contents                          |
| `#NewSecret`    | [secrets.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/secrets.cue) | Create a new a secret from a filesystem tree                          |
| `#TrimSecret`   | [secrets.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/secrets.cue) | Trim leading and trailing space characters from a secret              |

## Core Actions related to containers

| Definition      | File                                                                                              | Description                                                           |
| :--             | :--                                                                                               | :--                                                                   |
| `#Dockerfile`   | [image.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/image.cue)     | Build a container image using a Dockerfile                            |
| `#Exec`         | [exec.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/exec.cue)       | Execute a command in a docker-compatible container                    |
| `#Export`       | [image.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/image.cue)     | Export a docker image as a tar archive                                      |
| `#Pull`         | [image.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/image.cue)     | Download an image from a docker registry                              |
| `#Push`         | [image.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/image.cue)     | Upload an image to a docker registry                                  |
| `#Set`          | [image.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/image.cue)     | Modify a docker image config |

## Core Actions related to remote data sources

| Definition      | File                                                                                              | Description                                                           |
| :--             | :--                                                                                               | :--                                                                   |
| `#GitPull`      | [git.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/git.cue)         | Download a repository from a remote git server                        |
| `#HTTPFetch`    | [http.cue](https://github.com/dagger/dagger/blob/v0.2.4/pkg/dagger.io/dagger/core/http.cue)       | Get a file from an HTTP server                                        |
