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
    DAGGER_VERSION: 0.2.9
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

```yaml
apiVersion: tekton.dev/v1beta1
kind: Pipeline
metadata:
  name: dagger
spec:
  description: |
    Execute a dagger action from a git repo.
  params:
  - name: dagger-version
    type: string
    description: The dagger version to run.
  - name: dagger-action
    type: string
    description: The dagger action to execute.
  - name: repo-url
    type: string
    description: The git repository URL to clone from.
  - name: app-dir
    type: string
    description: The path to access the app dagger codebase within the repository.
  - name: netlify-site-name
    type: string
    description: The Netlify site name. Unique value among Netlify sites.
  - name: netlify-team
    type: string
    description: The Netlify team to deploy to.
  workspaces:
  - name: shared-data
    description: |
      This workspace will receive the cloned git repo and be passed
      to the next Task.

  tasks:

  - name: fetch-repo
    taskRef:
      name: git-clone
    workspaces:
    - name: output
      workspace: shared-data
    params:
    - name: url
      value: $(params.repo-url)
    - name: revision
      value: $(params.dagger-version)

  - name: dagger-do
    runAfter: ["fetch-repo"]  # Wait until the clone is done before deploying the app.
    workspaces:
    - name: source
      workspace: shared-data
    params:
      - name: dagger-version
        value: $(params.dagger-version)
      - name: dagger-action
        value: $(params.dagger-action)
      - name: app-dir
        value: $(params.app-dir)
      - name: netlify-site-name
        value: $(params.netlify-site-name)
      - name: netlify-team
        value: $(params.netlify-team)
    taskSpec:
      workspaces:
      - name: source
      params:
      - name: dagger-version
      - name: dagger-action
      - name: app-dir
      - name: netlify-site-name
      - name: netlify-team
      steps:
      - image: docker:20.10.13
        name: run-dagger-action
        workingDir: "$(workspaces.source.path)/$(params.app-dir)"
        script: |
          #!/usr/bin/env sh

          # Install dagger
          # Could be removed by using an official muli-arch dagger.io image
          arch=$(uname -m)
          case $arch in
            x86_64) arch="amd64" ;;
            aarch64) arch="arm64" ;;
          esac

          wget -c https://github.com/dagger/dagger/releases/download/$(params.dagger-version)/dagger_$(params.dagger-version)_linux_${arch}.tar.gz  -O - |  \
          tar zxf - -C /usr/local/bin

          dagger version
          dagger do $(params.dagger-action)

        env:
          - name: APP_NAME
            value: $(params.netlify-site-name)
          - name: NETLIFY_TEAM
            value: $(params.netlify-team)
          - name: DAGGER_LOG_FORMAT
            value: plain
          # Get one from https://app.netlify.com/user/applications/personal
          # and save it as a generic kubernetes secret
          - name: NETLIFY_TOKEN
            valueFrom:
              secretKeyRef:
                name: netlify
                key: token
        volumeMounts:
        - mountPath: /var/run/
          name: dind-socket
      sidecars:
        - image: docker:20.10.13-dind
          name: server
          securityContext:
            privileged: true
          volumeMounts:
          - mountPath: /var/run/
            name: dind-socket
      volumes:
      - name: dind-socket
        emptyDir: {}
---
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: deploy-todo-app
spec:
  pipelineRef:
    name: dagger
  workspaces:
  - name: shared-data
    volumeClaimTemplate:
      spec:
        accessModes:
        - ReadWriteOnce
        resources:
          requests:
            storage: 1Gi
  params:
  - name: dagger-version
    value: v0.2.6
  - name: dagger-action
    value: deploy
  - name: repo-url
    value: https://github.com/dagger/dagger.git
  - name: app-dir
    value: pkg/universe.dagger.io/examples/todoapp
  - name: netlify-site-name
    value: todoapp-dagger-europa
  - name: netlify-team
    value: dagger

```

</TabItem>

</Tabs>
