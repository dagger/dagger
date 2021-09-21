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

| Name                                    | Type                    | Description                                                                          |
| -------------                           |:-------------:          |:-------------:                                                                       |
|*source*                                 | `dagger.#Artifact`      |Application source code                                                               |
|*package*                                | `struct`                |Extra alpine packages to install                                                      |
|*cwd*                                    | `*"." \| string`        |working directory to use                                                              |
|*env*                                    | `struct`                |Environment variables                                                                 |
|*writeEnvFile*                           | `*"" \| string`         |Write the contents of `environment` to this file, in the "envfile" format             |
|*buildDir*                               | `*"build" \| string`    |Read build output from this directory (path must be relative to working directory)    |
|*script*                                 | `*"build" \| string`    |Run this yarn script                                                                  |
|*args*                                   | `*[] \| []`             |Optional arguments for the script                                                     |
|*build.from.image.package*               | `struct`                |List of packages to install                                                           |
|*build.from.env*                         | `struct`                |Environment variables shared by all commands                                          |
|*build.from.env.YARN_BUILD_SCRIPT*       | `*"build" \| string`    |-                                                                                     |
|*build.from.env.YARN_CWD*                | `*"." \| string`        |-                                                                                     |
|*build.from.env.YARN_BUILD_DIRECTORY*    | `*"build" \| string`    |-                                                                                     |
|*build.from.mount."/src".from*           | `dagger.#Artifact`      |-                                                                                     |
|*ctr.image.package*                      | `struct`                |List of packages to install                                                           |
|*ctr.env*                                | `struct`                |Environment variables shared by all commands                                          |
|*ctr.env.YARN_BUILD_SCRIPT*              | `*"build" \| string`    |-                                                                                     |
|*ctr.env.YARN_CWD*                       | `*"." \| string`        |-                                                                                     |
|*ctr.env.YARN_BUILD_DIRECTORY*           | `*"build" \| string`    |-                                                                                     |
|*ctr.mount."/src".from*                  | `dagger.#Artifact`      |-                                                                                     |

### yarn.#Package Outputs

| Name             | Type              | Description              |
| -------------    |:-------------:    |:-------------:           |
|*build*           | `struct`          |Build output directory    |
