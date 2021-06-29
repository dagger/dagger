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

| Name             | Type                    | Description                         |
| -------------    |:-------------:          |:-------------:                      |
|*source*          | `dagger.#Artifact`      |Application source code              |
|*package*         | `struct`                |Extra alpine packages to install     |
|*env*             | `struct`                |Environment variables                |
|*phases*          | `*["package"] \| []`    |-                                    |
|*goals*           | `*[] \| []`             |-                                    |
|*args*            | `*[] \| []`             |Optional arguments for the script    |

### maven.#Project Outputs

| Name             | Type              | Description              |
| -------------    |:-------------:    |:-------------:           |
|*build*           | `struct`          |Build output directory    |
