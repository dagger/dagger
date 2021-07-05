---
slug: /learn/106-cloudrun
---

# Dagger 106: deploy to Cloud Run

This tutorial illustrates how to use Dagger to build, push and deploy Docker images to Cloud Run.

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

## Initialize a Dagger Workspace and Environment

### (optional) Setup example app

You will need the local copy of the [Dagger examples repository](https://github.com/dagger/examples) used in previous guides

```shell
git clone https://github.com/dagger/examples
```

Make sure that all commands are being ran from the todoapp directory:

```shell
cd examples/todoapp
```

### (optional) Initialize a Cue module

In this guide we will use the same directory as the root of the Dagger workspace and the root of the Cue module; but you can create your Cue module anywhere inside the Dagger workspace.

```shell
cue mod init
```

### Organize your package

Let's create a new directory for our Cue package:

```shell
mkdir cue.mod/gcpcloudrun
```

### Create a basic plan

```cue title="todoapp/cue.mod/gcpcloudrun/source.cue"
package gcpcloudrun

import (
    "alpha.dagger.io/dagger"
    "alpha.dagger.io/docker"
    "alpha.dagger.io/gcp"
    "alpha.dagger.io/gcp/cloudrun"
    "alpha.dagger.io/gcp/gcr"
)

// Source code of the sample application
src: dagger.#Artifact & dagger.#Input

// GCR full image name
imageRef: string & dagger.#Input

image: docker.#Build & {
	source: src
}

gcpConfig: gcp.#Config

creds: gcr.#Credentials & {
	config: gcpConfig
}

push: docker.#Push & {
	target: imageRef
	source: image
	auth: {
		username: creds.username
		secret: creds.secret
	}
}

deploy: cloudrun.#Service & {
	config: gcpConfig
	image:  push.ref
}
```

## Set up the environment

### Create a new environment

Now that your Cue package is ready, let's create an environment to run it:

```shell
dagger new 'gcpcloudrun' -m cue.mod/gcpcloudrun
```

### Configure user inputs

```shell
dagger input dir src . -e gcpcloudrun
dagger input text deploy.name todoapp -e gcpcloudrun
dagger input text imageRef gcr.io/<your-project>/todoapp -e gcpcloudrun
dagger input text gcpConfig.region us-west2 -e gcpcloudrun
dagger input text gcpConfig.project <your-project> -e gcpcloudrun
dagger input secret gcpConfig.serviceKey -f ./gcp-sa-key.json -e gcpcloudrun
```

## Deploy

Now that everything is properly set, let's deploy on Cloud Run:

```shell
dagger up -e gcpcloudrun
```
