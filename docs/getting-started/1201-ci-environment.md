---
slug: /1201/ci-environment
displayed_sidebar: europa
---

# Integrating with your CI environment

Dagger can be used with any CI environment (no migration required) and has two important advantages which make the overall experience less error-prone and more efficient:

1. You don't write YAML [CUE](https://cuelang.org/) - typed configuration with built-in formatting
2. Configuration is executed in [BuildKit](https://github.com/moby/buildkit), the execution engine at the heart of Docker

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

```yaml file=../tests/getting-started/github-actions.yml title=".github/workflows/todoapp.yml"

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
