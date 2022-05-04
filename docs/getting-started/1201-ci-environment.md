---
slug: /1201/ci-environment
displayed_sidebar: '0.2'
---

# Integrating with your CI environment

Dagger can be used with any CI environment (no migration required) and has two important advantages which make the overall experience less error-prone and more efficient:

1. You don't write YAML, you write [CUE](https://cuelang.org/) - typed configuration with built-in formatting
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

```yaml
.docker:
  image: docker:${DOCKER_VERSION}-git
  services:
    - docker:${DOCKER_VERSION}-dind
  variables:
    # See https://docs.gitlab.com/ee/ci/docker/using_docker_build.html#docker-in-docker-with-tls-enabled-in-the-docker-executor
    DOCKER_HOST: tcp://docker:2376

    DOCKER_TLS_VERIFY: '1'
    DOCKER_TLS_CERTDIR: '/certs'
    DOCKER_CERT_PATH: '/certs/client'

    # Faster than the default, apparently
    DOCKER_DRIVER: overlay2

    DOCKER_VERSION: '20.10'

.dagger:
  extends: [.docker]
  variables:
    DAGGER_VERSION: 0.2.8
    DAGGER_LOG_FORMAT: plain
    DAGGER_CACHE_PATH: .dagger-cache

    ARGS: ''
  cache:
    key: dagger-${CI_JOB_NAME}
    paths:
      - ${DAGGER_CACHE_PATH}
  before_script:
    - apk add --no-cache curl
    - |
      # install dagger
      cd /usr/local
      curl -L https://dl.dagger.io/dagger/install.sh | sh
      cd -

      dagger version
  script:
    - dagger project update
    - |
      dagger \
          do \
          --cache-from type=local,src=${DAGGER_CACHE_PATH} \
          --cache-to type=local,mode=max,dest=${DAGGER_CACHE_PATH} \
          ${ARGS}

build:
  extends: [.dagger]
  variables:
    ARGS: build
```

</TabItem>

<TabItem value="jenkins">

<iframe width="800" height="450" style={{width: '100%', marginBottom: '2rem'}} src="https://youtube.com/embed/7u2A4etUuRY" title="YouTube video player" frameborder="0" allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; fullscreen"></iframe>

With `docker` client and `dagger` installed on your Jenkins agent, a Docker host available (can be `docker:dind`), and agents labeled in Jenkins with `dagger`:

```groovy
pipeline {
  agent { label 'dagger' }
  
  environment {
    //https://www.jenkins.io/doc/book/pipeline/jenkinsfile/#handling-credentials
    //DH_CREDS              = credentials('jenkins-dockerhub-creds')
    //AWS_ACCESS_KEY_ID     = credentials('jenkins-aws-secret-key-id')
    //AWS_SECRET_ACCESS_KEY = credentials('jenkins-aws-secret-access-key')
    //https://www.jenkins.io/doc/book/pipeline/jenkinsfile/#using-environment-variables
    GREETING = "Hello there, Jenkins! Hello"
  }
  stages {
    stage("do") {
      steps {
        //this example uses https://github.com/jpadams/helloworld-dagger-jenkins
        //if you're using your own Dagger plan, substitute your action name for 'hello'
        //e.g. 'build' or 'push' or whatever you've created!
        sh '''
            dagger do hello --log-format=plain
        '''
      }
    }
  }
}
```

</TabItem>

<TabItem value="tekton">

If you would like us to document Tekton next, vote for it here: [dagger#1677](https://github.com/dagger/dagger/discussions/1677)

</TabItem>

</Tabs>
