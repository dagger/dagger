---
slug: /114934/nodejs-ci
displayed_sidebar: "current"
category: "guides"
tags: ["nodejs", "gitlab-ci", "github-actions", "circle-ci", "jenkins"]
authors: ["Jeremy Adams"]
date: "13/12/2022"
---

# Dagger Node.js SDK in CI

import Tabs from '@theme/Tabs'; import TabItem from '@theme/TabItem';

<Tabs defaultValue="github-actions"
groupId="ci-environment"
values={[
{label: 'GitHub Actions', value: 'github-actions'},
{label: 'CircleCI', value: 'circleci'},
{label: 'GitLab', value: 'gitlab'},
{label: 'Jenkins', value: 'jenkins'},
]}>

<TabItem value="github-actions">

```yaml title=".github/workflows/dagger.yaml" file=./snippets/nodejs-ci/actions.yml
```

Ensure that your `package.json` contains `@dagger.io/dagger` which can be installed using the [documentation](../sdk/nodejs/835948/install).

</TabItem>

<TabItem value="circleci">

```yaml title=".circleci/config.yml" file=./snippets/nodejs-ci/circle.yml
```

Ensure that your `package.json` contains `@dagger.io/dagger` which can be installed using the [documentation](../sdk/nodejs/835948/install).

</TabItem>

<TabItem value="gitlab">

```yaml title=".gitlab-ci.yml" file=./snippets/nodejs-ci/gitlab.yml
```

Ensure that your `package.json` contains `@dagger.io/dagger` which can be installed using the [documentation](../sdk/nodejs/835948/install).

</TabItem>

<TabItem value="jenkins">

```groovy title="Jenkinsfile" file=./snippets/nodejs-ci/Jenkinsfile
```

Requires `docker` client and Node.js installed on your Jenkins agent, a Docker host available (can be `docker:dind`), and agents labeled in Jenkins with `dagger`. Ensure that your `package.json` contains `@dagger.io/dagger` which can be installed using the [documentation](../sdk/nodejs/835948/install).

</TabItem>

</Tabs>
