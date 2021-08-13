---
slug: /1006/google-cloud-run/
---

# Deploy to Google Cloud Run with Dagger

This tutorial illustrates how to use Dagger to build, push and deploy Docker images to Cloud Run.

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

## Initialize a Dagger Workspace and Environment

### (optional) Setup example app

You will need the local copy of the [Dagger examples repository](https://github.com/dagger/examples) used in previous guides

```shell
git clone https://github.com/dagger/examples
```

Make sure that all commands are being run from the todoapp directory:

```shell
cd examples/todoapp
```

### (optional) Initialize a Cue module

This guide will use the same directory as the root of the Dagger workspace and the root of the Cue module, but you can create your Cue module anywhere inside the Dagger workspace.

```shell
cue mod init
```

### Organize your package

Let's create a new directory for our Cue package:

```shell
mkdir gcpcloudrun
```

### Create a basic plan

```cue file=./tests/gcpcloudrun/source.cue title="todoapp/cue.mod/gcpcloudrun/source.cue"
```

## Set up the environment

### Create a new environment

Now that your Cue package is ready, let's create an environment to run it:

```shell
dagger new 'gcpcloudrun' -p ./gcpcloudrun
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

Now that everything is set correctly, let's deploy on Cloud Run:

```shell
dagger up -e gcpcloudrun
```
