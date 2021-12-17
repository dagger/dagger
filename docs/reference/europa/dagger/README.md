---
sidebar_label: dagger
---

# alpha.dagger.io/europa/dagger

```cue
import "alpha.dagger.io/europa/dagger"
```

## dagger.#DAG

A special kind of program which `dagger` can execute.

### dagger.#DAG Inputs

_No input._

### dagger.#DAG Outputs

_No output._

## dagger.#FS

A reference to a filesystem tree. For example:  - The root filesystem of a container  - A source code repository  - A directory containing binary artifacts Rule of thumb: if it fits in a tar archive, it fits in a #FS.

### dagger.#FS Inputs

_No input._

### dagger.#FS Outputs

_No output._

## dagger.#Plan

A deployment plan executed by `dagger up`

### dagger.#Plan Inputs

_No input._

### dagger.#Plan Outputs

_No output._

## dagger.#Secret

A reference to an external secret, for example:  - A password  - A SSH private key  - An API token Secrets are never merged in the Cue tree. They can only be used by a special filesystem mount designed to minimize leak risk.

### dagger.#Secret Inputs

_No input._

### dagger.#Secret Outputs

_No output._

## dagger.#Service

A reference to a network service endpoint, for example:  - A TCP or UDP port  - A unix socket  - An HTTPS endpoint

### dagger.#Service Inputs

_No input._

### dagger.#Service Outputs

_No output._

## dagger.#Stream

A stream of bytes

### dagger.#Stream Inputs

_No input._

### dagger.#Stream Outputs

_No output._

## dagger.#Subdir

Select a subdirectory from a filesystem tree

### dagger.#Subdir Inputs

_No input._

### dagger.#Subdir Outputs

_No output._
