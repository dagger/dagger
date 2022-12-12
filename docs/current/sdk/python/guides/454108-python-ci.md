---
slug: /454108/python-ci
displayed_sidebar: "current"
---

# Dagger Python SDK in CI

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

```yaml title=".github/workflows/dagger.yaml" file=../snippets/python-ci/actions.yml
```

</TabItem>

<TabItem value="circleci">

```yaml title=".circleci/config.yml" file=../snippets/python-ci/circle.yml
```

</TabItem>

<TabItem value="gitlab">

```yaml title=".gitlab-ci.yml" file=../snippets/python-ci/gitlab.yml
```

</TabItem>

<TabItem value="jenkins">

```groovy title="Jenkinsfile" file=../snippets/python-ci/Jenkinsfile
```

Requires `docker` client and `python` installed on your Jenkins agent, a Docker host available (can be `docker:dind`), and agents labeled in Jenkins with `dagger`.

</TabItem>

</Tabs>
