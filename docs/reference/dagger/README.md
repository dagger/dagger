---
sidebar_label: dagger
---

# alpha.dagger.io/dagger

Dagger core types

```cue
import "alpha.dagger.io/dagger"
```

## dagger.#Secret

A reference to an external secret, for example:  - A password  - A SSH private key  - An API token Secrets are never merged in the Cue tree. They can only be used by a special filesystem mount designed to minimize leak risk.

### dagger.#Secret Inputs

_No input._

### dagger.#Secret Outputs

_No output._

## dagger.#Stream

Dagger stream. Can be mounted as a UNIX socket.

### dagger.#Stream Inputs

_No input._

### dagger.#Stream Outputs

_No output._
