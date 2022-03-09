---
slug: /1201/ci-environment
displayed_sidebar: europa
---

# From local dev to CI environment

Dagger can be used with any CI environment (no migration required) and has two important advantages which make the overall experience less error-prone and more efficient:

1. Instead of YAML you write CUE: typed configuration with built-in formatting
2. Configuration is executed in buildkit, the execution engine at the heart of Docker

This makes any CI environment with Docker pre-installed work with Dagger out of the box.
We started with [CI environments that you told us you are using](https://github.com/dagger/dagger/discussions/1677).
We will configure a production deployment for the same application that we covered in the previous page.

:::note
If you cannot find your CI environment below, [let us know via this GitHub discussion](https://github.com/dagger/dagger/discussions/1677).
:::

import Tabs from '@theme/Tabs'; import TabItem from '@theme/TabItem';

<Tabs defaultValue="github-actions"
groupId="ci-environment"
values={[
{label: 'GitHub Actions', value: 'github-actions'},
{label: 'CircleCI', value: 'circleci'},
{label: 'GitLab', value: 'gitlab'},
{label: 'Jenkins', value: 'jenkins'},
{label: 'Tekton', value: 'tekton'},
]}>

<TabItem value="github-actions">

`.github/workflows/todoapp.yml`

```yaml
name: todoapp

push:
  # Trigger this workflow only on commits pushed to the main branch
  branches:
    - main

# Dagger plan gets configured via client environment variables
env:
  # This needs to be unique across all of netlify.app
  APP_NAME: todoapp-dagger-europa
  NETLIFY_TEAM: dagger
  # https://app.netlify.com/user/applications/personal
  NETLIFY_TOKEN: ${{ secrets.NETLIFY_TOKEN }}
  DAGGER_LOG_FORMAT: plain

jobs:
  dagger:
    runs-on: ubuntu-latest
    steps:
      - name: Clone repository
        uses: actions/checkout@v2

      - name: Deploy to Netlify
        # https://github.com/dagger/dagger-for-github
        uses: dagger/dagger-for-github@v2
        with:
          workdir: pkg/universe.dagger.io/examples/todoapp
          args: do deploy
```

</TabItem>

<TabItem value="circleci">

If you would like us to document CircleCI next, vote for it here: [dagger#1677](https://github.com/dagger/dagger/discussions/1677)

</TabItem>

<TabItem value="gitlab">

If you would like us to document GitLab next, vote for it here: [dagger#1677](https://github.com/dagger/dagger/discussions/1677)

</TabItem>

<TabItem value="jenkins">

If you would like us to document Jenkins next, vote for it here: [dagger#1677](https://github.com/dagger/dagger/discussions/1677)

</TabItem>

<TabItem value="tekton">

If you would like us to document Tekton next, vote for it here: [dagger#1677](https://github.com/dagger/dagger/discussions/1677)

</TabItem>

</Tabs>
