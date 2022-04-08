---
slug: /1225/pushing-plan-dependencies/
---

# Pushing your plan's dependencies

After completing your plan and setting up your GHA or Gitlab CI, you'll realize that a lot of `Cue` files are present in the `cue.mod/pkg` directory. These are the dependencies required by Dagger to run your actions :

```shell
cue.mod/
├── pkg
│   ├── dagger.io
│   │   ├── cue.mod
│   │   └── dagger
│   │       └── core
│   └── universe.dagger.io
│       ├── alpine
│       │   └── test
│       ├── aws
│       │   ├── cli
│       │   │   └── test
│       │   └── test
│       ├── bash
│       │   └── test
│       │       └── data
│       ├── cue.mod
│       ├── docker
│       │   ├── cli
│       │   │   └── test
│       │   └── test
│       ├── examples
│       │   ├── changelog.com
│       │   │   ├── elixir
│       │   │   │   └── mix
│       │   │   └── gerhard
│       │   ├── helloworld
│       │   └── todoapp
│       │       ├── public
│       │       └── src
│       │           └── components
│       ├── git
│       ├── go
│       │   └── test
│       ├── netlify
│       │   └── test
│       │       └── testutils
│       ├── nginx
│       ├── powershell
│       │   └── test
│       │       └── data
│       ├── python
│       ├── x
│       │   └── david@rawkode.dev
│       │       └── pulumi
│       └── yarn
│           └── test
│               └── data
│                   ├── bar
│                   └── foo
└── usr
```

The current best practice is to push your project with these files: it will ensure its consistency between runs.

:::info
We are aware of that and, soon, `dagger project update` will only download the dependencies required by your actions
:::
