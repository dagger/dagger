---
sidebar_label: maven
---

# alpha.dagger.io/java/maven

Maven is a build automation tool for Java

```cue
import "alpha.dagger.io/java/maven"
```

## maven.#Project

A Maven project

### maven.#Project Inputs

| Name                         | Type                    | Description                                    |
| -------------                |:-------------:          |:-------------:                                 |
|*source*                      | `dagger.#Artifact`      |Application source code                         |
|*package*                     | `struct`                |Extra alpine packages to install                |
|*env*                         | `struct`                |Environment variables                           |
|*phases*                      | `*["package"] \| []`    |-                                               |
|*goals*                       | `*[] \| []`             |-                                               |
|*args*                        | `*[] \| []`             |Optional arguments for the script               |
|*build.from.image.package*    | `struct`                |List of packages to install                     |
|*build.from.env*              | `struct`                |Environment variables shared by all commands    |
|*build.from.copy."/".from*    | `dagger.#Artifact`      |-                                               |
|*ctr.image.package*           | `struct`                |List of packages to install                     |
|*ctr.env*                     | `struct`                |Environment variables shared by all commands    |
|*ctr.copy."/".from*           | `dagger.#Artifact`      |-                                               |

### maven.#Project Outputs

| Name             | Type              | Description              |
| -------------    |:-------------:    |:-------------:           |
|*build*           | `struct`          |Build output directory    |
