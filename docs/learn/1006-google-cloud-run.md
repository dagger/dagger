---
slug: /1006/google-cloud-run/
---

<!--
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
!!! OLD DOCS. NOT MAINTAINED. !!!
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
-->

import CautionBanner from '../\_caution-banner.md'

# Deploy to Google Cloud Run with Dagger

<CautionBanner old="0.1" new="0.2" />

This tutorial illustrates how to use Dagger to build, push and deploy Docker images to Cloud Run.

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

## Initialize a Dagger Project and Environment

### (optional) Setup example app

You will need the local copy of the [Dagger examples repository](https://github.com/dagger/examples) used in previous guides

```shell
git clone https://github.com/dagger/examples
```

Make sure that all commands are being run from the todoapp directory:

```shell
cd examples/todoapp
```

### Organize your package

Let's create a new directory for our Cue package:

```shell
mkdir gcpcloudrun
```

### Create a basic plan

```cue file=./tests/gcpcloudrun/source.cue title="todoapp/gcpcloudrun/source.cue"

```

## Set up the environment

### Create a new environment

Let's create a project:

```shell
dagger init
```

Let's create an environment to run it:

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
