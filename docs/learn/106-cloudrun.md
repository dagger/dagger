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

<!-- git clone https://github.com/dagger/examples -->
```shell file=./tests/helpers.bash#L45
```

Make sure that all commands are being run from the todoapp directory:

<!-- cd examples/todoapp -->
```shell file=./tests/helpers.bash#L47
```

### (optional) Initialize a Cue module

This guide will use the same directory as the root of the Dagger workspace and the root of the Cue module, but you can create your Cue module anywhere inside the Dagger workspace.

<!-- cue mod init -->
```shell file=./tests/helpers.bash#L48
```

### Organize your package

Let's create a new directory for our Cue package:

<!-- mkdir gcpcloudrun -->
```shell file=./tests/doc.bats#L63
```

### Create a basic plan

```cue file=./tests/gcpcloudrun/source.cue title="todoapp/cue.mod/gcpcloudrun/source.cue"
```

## Set up the environment

### Create a new environment

Now that your Cue package is ready, let's create an environment to run it:

<!-- dagger new 'gcpcloudrun' -p gcpcloudrun -->
```shell file=./tests/doc.bats#L67
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
