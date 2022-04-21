---
slug: /1009/github-actions/
---

import CautionBanner from '../\_caution-banner.md'

# Integrate Dagger with GitHub Actions

<CautionBanner old="0.1" new="0.2" />

This tutorial illustrates how to use GitHub Actions and Dagger to build, push and deploy Docker images to Cloud Run.

## Prerequisites

We assume that you've finished our 106-cloudrun tutorial as this one continues right after.

## Setup new GitHub repo

Push existing `examples/todoapp` directory to your new GitHub repo (public or private). It should contain all the code
from `https://github.com/dagger/examples/tree/main/todoapp`, `gcpcloudrun` and `.dagger/env/gcpcloudrun/` directory.

### Add GitHub Actions Secret

Dagger encrypts all input secrets using your key stored at `~/.config/dagger/keys.txt`. Copy the entire line starting
with `AGE-SECRET-KEY-` and save it to a GitHub secret named `DAGGER_AGE_KEY`. In case you don't know how to create
secrets on GitHub take a look at [this tutorial](https://docs.github.com/en/actions/reference/encrypted-secrets).

## Create a GitHub Actions Workflow

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
