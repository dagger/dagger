---
sidebar_label: yarn
---

# alpha.dagger.io/js/yarn

Yarn is a package manager for Javascript applications

```cue
import "alpha.dagger.io/js/yarn"
```

## yarn.#Package

A Yarn package

### yarn.#Package Inputs

| Name             | Type                    | Description                                                                          |
| -------------    |:-------------:          |:-------------:                                                                       |
|*source*          | `dagger.#Artifact`      |Application source code                                                               |
|*package*         | `struct`                |Extra alpine packages to install                                                      |
|*cwd*             | `*"." \| string`        |working directory to use                                                              |
|*env*             | `struct`                |Environment variables                                                                 |
|*writeEnvFile*    | `*"" \| string`         |Write the contents of `environment` to this file, in the "envfile" format             |
|*buildDir*        | `*"build" \| string`    |Read build output from this directory (path must be relative to working directory)    |
|*script*          | `*"build" \| string`    |Run this yarn script                                                                  |
|*args*            | `*[] \| [...string]`    |Optional arguments for the script                                                     |

### yarn.#Package Outputs

| Name             | Type              | Description              |
| -------------    |:-------------:    |:-------------:           |
|*build*           | `struct`          |Build output directory    |
