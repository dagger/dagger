---
sidebar_label: engine
---

# alpha.dagger.io/europa/dagger/engine

```cue
import "alpha.dagger.io/europa/dagger/engine"
```

## engine.#Build

Build a container image using buildkit

### engine.#Build Inputs

_No input._

### engine.#Build Outputs

_No output._

## engine.#CacheDir

A (best effort) persistent cache dir

### engine.#CacheDir Inputs

_No input._

### engine.#CacheDir Outputs

_No output._

## engine.#Copy

Copy files from one FS tree to another

### engine.#Copy Inputs

_No input._

### engine.#Copy Outputs

_No output._

## engine.#CopyInfo

### engine.#CopyInfo Inputs

_No input._

### engine.#CopyInfo Outputs

_No output._

## engine.#DAG

A special kind of program which `dagger` can execute.

### engine.#DAG Inputs

_No input._

### engine.#DAG Outputs

_No output._

## engine.#Exec

Execute a command in a container

### engine.#Exec Inputs

_No input._

### engine.#Exec Outputs

_No output._

## engine.#FS

A reference to a filesystem tree. For example:  - The root filesystem of a container  - A source code repository  - A directory containing binary artifacts Rule of thumb: if it fits in a tar archive, it fits in a #FS.

### engine.#FS Inputs

_No input._

### engine.#FS Outputs

_No output._

## engine.#GitPull

Pull a directory from a git remote Warning: do NOT embed credentials in the remote url as this will expose them in logs. By using username and password Dagger will handle this for you in a secure manner.

### engine.#GitPull Inputs

_No input._

### engine.#GitPull Outputs

_No output._

## engine.#GitPush

Push a directory to a git remote

### engine.#GitPush Inputs

_No input._

### engine.#GitPush Outputs

_No output._

## engine.#HTTPFetch

Fetch a file over HTTP

### engine.#HTTPFetch Inputs

_No input._

### engine.#HTTPFetch Outputs

_No output._

## engine.#ImageConfig

Container image config. See [OCI](https://www.opencontainers.org/). Spec left open on purpose to account for additional fields. [Image Spec](https://github.com/opencontainers/image-spec/blob/main/specs-go/v1/config.go) [Docker Superset](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/dockerfile2llb/image.go)

### engine.#ImageConfig Inputs

_No input._

### engine.#ImageConfig Outputs

_No output._

## engine.#LoadSecret

Load a secret from a filesystem tree

### engine.#LoadSecret Inputs

_No input._

### engine.#LoadSecret Outputs

_No output._

## engine.#Merge

Merge multiple FS trees into one

### engine.#Merge Inputs

_No input._

### engine.#Merge Outputs

_No output._

## engine.#Mkdir

Create one or multiple directory in a container

### engine.#Mkdir Inputs

_No input._

### engine.#Mkdir Outputs

_No output._

## engine.#Mount

A transient filesystem mount.

### engine.#Mount Inputs

_No input._

### engine.#Mount Outputs

_No output._

## engine.#Plan

A deployment plan executed by `dagger up`

### engine.#Plan Inputs

_No input._

### engine.#Plan Outputs

_No output._

## engine.#Pull

Download a container image from a remote repository

### engine.#Pull Inputs

_No input._

### engine.#Pull Outputs

_No output._

## engine.#Push

Upload a container image to a remote repository

### engine.#Push Inputs

_No input._

### engine.#Push Outputs

_No output._

## engine.#ReadFile

### engine.#ReadFile Inputs

_No input._

### engine.#ReadFile Outputs

_No output._

## engine.#Scratch

Produce an empty directory

### engine.#Scratch Inputs

_No input._

### engine.#Scratch Outputs

_No output._

## engine.#Secret

A reference to an external secret, for example:  - A password  - A SSH private key  - An API token Secrets are never merged in the Cue tree. They can only be used by a special filesystem mount designed to minimize leak risk.

### engine.#Secret Inputs

_No input._

### engine.#Secret Outputs

_No output._

## engine.#Service

A reference to a network service endpoint, for example:  - A TCP or UDP port  - A unix or npipe socket  - An HTTPS endpoint

### engine.#Service Inputs

_No input._

### engine.#Service Outputs

_No output._

## engine.#TempDir

A temporary directory for command execution

### engine.#TempDir Inputs

_No input._

### engine.#TempDir Outputs

_No output._

## engine.#WriteFile

Write a file to a filesystem tree, creating it if needed

### engine.#WriteFile Inputs

_No input._

### engine.#WriteFile Outputs

_No output._
