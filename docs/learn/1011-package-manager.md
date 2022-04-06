---
slug: /1011/package-manager/
---

# Manage packages using the package manager

This tutorial illustrates how to install and upgrade packages using Dagger package manager.

## Installing a package

### Initializing project

Create an empty directory for your new Dagger project:

```shell
mkdir project
cd project
```

As described in the previous tutorials, initialize your Dagger project:

```shell
dagger init
dagger new test
```

That will create 2 directories: `.dagger` and `cue.mod` where our package will reside:

```shell
.
├── cue.mod
│   ├── module.cue
│   ├── pkg
│   └── usr
├── .dagger
│   └── env
│       └── test
```

### Install

In our example we will use `gcpcloudrun` package from [github](https://github.com/tjovicic/dagger-modules/tree/main/gcpcloudrun)
Let's first add it to our `source.cue` file:

```cue title="./source.cue"
package main

import (
  "github.com/dagger/packages/gcpcloudrun"
)

run: gcpcloudrun.#Run
```

To install it just run

```shell
dagger mod get github.com/dagger/packages/gcpcloudrun@v0.1
```

It should pull the `v0.1` version from GitHub, leave a copy in `cue.mod/pkg` and reflect the change in
`cue.mod/dagger.mod` file:

```shell
cue.mod/pkg/github.com/
└── dagger
    └── packages
        └── gcpcloudrun
            ├── cue.mod
            ├── README.md
            └── source.cue
```

```cue title="./cue.mod/dagger.mod"
github.com/dagger/packages/gcpcloudrun v0.1
```

Querying the current setup with `dagger query` should return a valid result:

```json
{
  "run": {
    "creds": {
      "username": "oauth2accesstoken"
    },
    "deploy": {
      "platform": "managed",
      "port": "80"
    },
    "push": {
      "auth": {
        "username": "oauth2accesstoken"
      },
      "push": {}
    }
  }
}
```

### Upgrading

Now that you've successfully installed a package, let's try to upgrade it.

```shell
dagger mod get github.com/dagger/packages/gcpcloudrun@v0.2
```

You should see similar output:

```shell
12:25PM INF system | downloading github.com/dagger/packages:v0.2
```

And `cue.mod/dagger.mod` should reflect the new version:

```cue title="./cue.mod/dagger.mod"
github.com/dagger/packages/gcpcloudrun v0.2
```

## Develop package locally

Currently, package manager cannot add local packages so a workaround is linking the package to `cue.mod/pkg`.
Create a directory with your domain name, usually github.com/myuser, and link your package directory.

```shell
mkdir cue.mod/pkg/<mydomain>
ln -s <localpackage> cue.mod/pkg/<mydomain>/<mypackagename>
```
