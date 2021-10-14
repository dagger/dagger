---
slug: /1010/dev-cue-package/
---

# Develop a new CUE package for Dagger

This tutorial illustrates how to create new packages, manually distribute them among your applications and contribute to the Dagger stdlib packages.

## Creating your package

### Initializing project

Create an empty directory for your new Dagger project:

```shell
mkdir project
cd project
```

As described in the previous tutorials, initialize your Dagger project:

```shell
dagger init
```

That will create two directories: `.dagger` and `cue.mod`, where our package will reside:

```shell
.
├── cue.mod
│   ├── module.cue
│   ├── pkg
│   └── usr
├── .dagger
│   └── env
```

### Writing the package

Now that you've initialized your project, it's time to write a simple package. Package name usually starts with a
domain name (as in Go) followed by a descriptive name. In this example, we reuse the Cloud Run example and create a
package from it.

```shell
mkdir -p cue.mod/pkg/github.com/username/gcpcloudrun
```

Let's write the package logic. It is basically what we've seen in the 106-cloudrun example:

```shell
touch cue.mod/pkg/github.com/username/gcpcloudrun/source.cue
```

```cue file=./tests/dev-cue-package/source.cue title="cue.mod/pkg/github.com/tjovicic/gcpcloudrun/source.cue"
```

### Running the package

Now that you've successfully created a package let's run it in a new environment. Create a new test package using
our reusable `gcpcloudrun`:

```cue file=./tests/dev-cue-package/script.sh#L3-L16
```

Run it:

```shell
dagger up -e staging
```

You should see a familiar output:

```shell
9:32AM ERR system | required input is missing    input=run.src
9:32AM ERR system | required input is missing    input=run.imageRef
9:32AM ERR system | required input is missing    input=run.gcpConfig.region
9:32AM ERR system | required input is missing    input=run.gcpConfig.project
9:32AM ERR system | required input is missing    input=run.gcpConfig.serviceKey
9:32AM ERR system | required input is missing    input=run.deploy.name
9:32AM FTL system | some required inputs are not set, please re-run with `--force` if you think it's a mistake    missing=0s
```

## Manually distributing packages

You've probably guessed this package isn't tied to just your project. You can easily copy/paste it into any number
of different projects and use it as we've shown above.

```shell
mkdir -p /my-new-workspace/cue.mod/pkg/github.com/username/gcpcloudrun
cp ./cue.mod/pkg/github.com/username/gcpcloudrun/source.cue /new-workspace/cue.mod/pkg/github.com/username/gcpcloudrun
```

## Contributing to Dagger stdlib

Our [stdlib](https://github.com/dagger/dagger/tree/main/stdlib) has many valuable packages that you can use.
You've probably seen it when you've initialized your project:

```shell
.
├── cue.mod
│   ├── module.cue
│   ├── pkg
│   │   ├── alpha.dagger.io
│   │   └── .gitignore
│   └── usr
```

We are still a small community and are constantly looking for new contributors that will work with us to improve this fantastic project. If you feel like we are missing a package or want to improve an existing one, please start with our
[contributing docs](https://github.com/dagger/dagger/blob/main/CONTRIBUTING.md) and open a PR.
