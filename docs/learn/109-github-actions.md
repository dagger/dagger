---
slug: /learn/109-github-actions
---

# Dagger 109: integrate with Github Actions

This tutorial illustrates how to use Github Actions and Dagger to build, push and deploy Docker images to Cloud Run.

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

## Prerequisites

We assume that you've finished our 106-cloudrun tutorial as this one continues right after.

## Setup new Github repo

Push existing `examples/todoapp` directory to your new Github repo (public or private). It should contain all the code
from `https://github.com/dagger/examples/tree/main/todoapp`, `gcpcloudrun` and `.dagger/env/gcpcloudrun/` directory.

### Add Github Actions Secret

Dagger encrypts all input secrets using your key stored at `~/.config/dagger/keys.txt`. Copy the entire line starting
with `AGE-SECRET-KEY-` and save it to a Github secret named `DAGGER_AGE_KEY`. In case you don't know how to create
secrets on Github take a look at [this tutorial](https://docs.github.com/en/actions/reference/encrypted-secrets).

## Create a Github Actions Workflow

Create `.github/workflows/gcpcloudrun.yml` file and paste the following code into it:

```yaml title=".github/workflows/gcpcloudrun.yml"
name: CloudRun

on:
  push:
    branches:
      - main

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v2
      - name: Dagger
        uses: dagger/dagger-action@v1
        with:
          age-key: ${{ secrets.DAGGER_AGE_KEY }}
          args: up -e gcpcloudrun
```

## Run

On any push to `main` branch this workflow should run and deploy the `todoapp` to your GCP Cloud Run instance.
