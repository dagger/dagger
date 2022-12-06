---
slug: /sdk/cue/470907/get-started
---

import CodeBlock from "@theme/CodeBlock";
import styles from "@site/src/css/install.module.scss";
import TutorialCard from "@site/src/components/molecules/tutorialCard.js";
import Button from "@site/src/components/atoms/button.js";

# Get Started with the Dagger CUE SDK

## Introduction

This tutorial teaches you the basics of using Dagger with CUE. You will learn how to:

- Install the CUE SDK
- Use the CUE SDK to build and test an application locally
- Use the CUE SDK to build and deploy the application remotely

## Requirements

This tutorial assumes that:

- You have a basic understanding of the CUE language. If not, [read the CUE tutorial](https://cuelang.org/docs/tutorials/tour/intro/).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Step 1: Install the Dagger CUE SDK

{@include: ../../../partials/_install-sdk-cue.md}

## Step 2: Build, run and test locally

Everyone should be able to develop, test and run their application using a local pipeline. Having to commit and push in order to test a change slows down iteration. This step explains how to use the Dagger CUE SDK to configure a local pipeline to build, run and test an application.

<BrowserOnly>
{() =>
<Tabs defaultValue={
 window.navigator.userAgent.indexOf('Linux') != -1 ? 'linux':
 window.navigator.userAgent.indexOf('Win') != -1 ? 'windows':
 'macos'}
groupId="os"
values={[
{label: 'macOS', value: 'macos'}, {label: 'Linux', value: 'linux'}, {label: 'Windows', value: 'windows'},
]}>

<TabItem value="macos">

{@include: ../../../partials/_get-started-cue-first-run.md}

With an empty cache, installing all dependencies, then testing and generating a build for this example application completes in just under 3 minutes:

```shell
[✔] client.filesystem."./".read                                   0.1s
[✔] actions.deps                                                118.8s
[✔] actions.test.script                                           0.1s
[✔] actions.test                                                  6.3s
[✔] actions.build.run.script                                      0.0s
[✔] actions.build.run                                            43.7s
[✔] actions.build.contents                                        0.4s
[✔] client.filesystem."./build".write                            0.1s
```

Since this is a static application, you can open the files which are generated in `actions.build.contents` in a browser. The last step - `client.filesystem.build.write` - copies the build result into the `build` directory on the host.

On macOS, run `open build/index.html` in your terminal and see the following application preview:

![todoapp preview](/img/getting-started/todoapp.macos.png)

{@include: ../../../partials/_get-started-cue-modify-code.md}

```shell
dagger-cue do build

[✔] client.filesystem."./".read                                   0.0s
[✔] actions.deps                                                  7.5s
[✔] actions.test.script                                           0.0s
[✔] actions.test                                                  6.0s
[✔] actions.build.run.script                                      0.0s
[✔] actions.build.run                                            29.2s
[✔] actions.build.contents                                        0.0s
[✔] client.filesystem."./build".write                            0.1s
```

The total `42.8` time is macOS specific, since the Linux alternative is more than 8x quicker. Either way, this local test and build loop is likely to change your approach to iterating on changes. It becomes even more obvious when the change is not as straightforward as knowing _exactly_ which line to edit.

</TabItem>

<TabItem value="linux">

{@include: ../../../partials/_get-started-cue-first-run.md}

With an empty cache, installing all dependencies, then testing and generating a build for this example application completes in just under 1 minute:

```shell
[✔] client.filesystem."./".read                                   0.3s
[✔] actions.deps                                                 39.7s
[✔] actions.test.script                                           0.2s
[✔] actions.test                                                  1.9s
[✔] actions.build.run.script                                      0.1s
[✔] actions.build.run                                            10.0s
[✔] actions.build.contents                                        0.6s
[✔] client.filesystem."./build".write                            0.1s
```

Since this is a static application, you can open the files which are generated in `actions.build.contents` in a browser. The last step - `client.filesystem.build.write` - copies the build result into the `build` directory on the host.

On Linux, run `xdg-open build/index.html` in your terminal and see the following application preview:

![todoapp preview](/img/getting-started/todoapp.linux.png)

{@include: ../../../partials/_get-started-cue-modify-code.md}

```shell
dagger-cue do build

[✔] client.filesystem."./".read                                   0.0s
[✔] actions.deps                                                  1.1s
[✔] actions.test.script                                           0.0s
[✔] actions.test                                                  0.0s
[✔] actions.build.run.script                                      0.8s
[✔] actions.build.run                                             2.9s
[✔] actions.build.contents                                        0.0s
[✔] client.filesystem."./build".write                             0.0s
```

Being able to re-run the test and build loop locally in `4.8s`, at the same speed as running `yarn` scripts locally and without adding any extra dependencies to our host, is likely to change your approach to iterating on changes. It becomes even more obvious when the change is not as straightforward as knowing _exactly_ which line to edit.

</TabItem>

<TabItem value="windows">

{@include: ../../../partials/_get-started-cue-first-run.md}

:::tip
By default, Git on Windows does not automatically convert POSIX symbolic links. To perform this conversion, add the extra option `core.symlinks=true` while cloning the repository. You can also enable this once and for all in your Git configuration, by running the following command from a Powershell terminal: `git config --global core.symlinks true`.

If you get a `Permission denied` error on Windows, [see different options to overcome this error](https://github.com/git-for-windows/git/wiki/Symbolic-Links#allowing-non-administrators-to-create-symbolic-links).
:::

With an empty cache, installing all dependencies, then testing & generating a build for this example application completes in just under a minute:

```shell
[✔] actions.deps                                                 62.1s
[✔] actions.build.run.script                                      0.4s
[✔] actions.test.script                                           0.5s
[✔] client.filesystem."./".read                                   0.6s
[✔] actions.test                                                  2.0s
[✔] actions.build.run                                            12.4s
[✔] actions.build.contents                                        0.1s
[✔] client.filesystem."./build".write                            0.2s
```

Since this is a static application, you can open the files which are generated in `actions.build.contents` in a browser. The last step - `client.filesystem.build.write` - copies the build result into the `build` directory on the host.

On Windows, run `start build/index.html` in your `Command Prompt` terminal and see the following application preview:

![todoapp preview](/img/getting-started/todoapp.macos.png)

{@include: ../../../partials/_get-started-cue-modify-code.md}

```shell
dagger-cue do build
[✔] actions.build.run.script                                     0.0s
[✔] actions.deps                                                 3.4s
[✔] client.filesystem."./".read                                  0.1s
[✔] actions.test.script                                          0.0s
[✔] actions.test                                                 1.8s
[✔] actions.build.run                                            7.7s
[✔] actions.build.contents                                       0.2s
[✔] client.filesystem."./build".write                           0.2s
```

Being able to re-run the test and build loop locally in `13.6s`, without adding any extra dependencies to the host, is likely to change your approach to iterating on changes. It becomes even more obvious when the change is not as straightforward as knowing _exactly_ which line to edit.

</TabItem>

</Tabs>
}

</BrowserOnly>

## Step 3: Build, run and test in a remote CI environment

Once you have the Dagger Engine running locally, it's easy to use Dagger with any CI environment (no migration required) to run the same Dagger pipelines. Any CI environment with Docker pre-installed works with Dagger out of the box.

Now that you are comfortable with the local CI/CD loop, the next step is to configure a production deployment of the application using a remote CI environment. This step will also deploy the build output to Netlify.

:::note
We started with [CI environments that you told us you are using](https://github.com/dagger/dagger/discussions/1677). If you cannot find your CI environment below, [let us know via this GitHub discussion](https://github.com/dagger/dagger/discussions/1677).
:::

<Tabs defaultValue="github-actions"
groupId="ci-environment"
values={[
{label: 'GitHub Actions', value: 'github-actions'},
{label: 'TravisCI', value: 'travisci'},
{label: 'CircleCI', value: 'circleci'},
{label: 'GitLab', value: 'gitlab'},
{label: 'Jenkins', value: 'jenkins'},
{label: 'Tekton', value: 'tekton'},
{label: 'AzurePipelines', value: 'azure-pipelines'},
]}>

<TabItem value="github-actions">

```yaml file=../tests/getting-started/github-actions.yml title=".github/workflows/todoapp.yml"

```

</TabItem>

<TabItem value="travisci">

```yaml title=".travis.yml"
os: linux
arch: amd64
dist: bionic
language: minimal

env:
  global:
    - DAGGER_VERSION: 0.2.25
    - DAGGER_LOG_FORMAT: plain
    - DAGGER_CACHE_PATH: .dagger-cache

services:
  - docker

install:
  - |
    # Installing dagger-cue
    cd /usr/local
    curl -L https://dl.dagger.io/dagger/install.sh | sudo sh
    cd -

stages:
  - name: build
    if: type IN (push, pull_request)

jobs:
  include:
    - stage: build
      env:
        - TASK: "dagger-cue do build"
      before_script:
        - dagger-cue project update
      script:
        - dagger-cue do build
```

</TabItem>

<TabItem value="circleci">

```yaml title=".circleci/config.yml"
version: 2.1

jobs:
  install-and-run-dagger:
    docker:
      - image: cimg/base:stable
    steps:
      - checkout
      - setup_remote_docker:
          version: "20.10.14"
      - run:
          name: "Install the Dagger Engine"
          command: |
            cd /usr/local
            wget -O - https://dl.dagger.io/dagger/install.sh | sudo sh
            cd -
      - run:
          name: "Run the Dagger Engine"
          command: |
            dagger-cue do build --log-format plain

workflows:
  dagger-workflow:
    jobs:
      - install-and-run-dagger
```

</TabItem>

<TabItem value="gitlab">

```yaml title=".gitlab-ci.yml"
.docker:
  image: docker:${DOCKER_VERSION}-git
  services:
    - docker:${DOCKER_VERSION}-dind
  variables:
    # See https://docs.gitlab.com/ee/ci/docker/using_docker_build.html#docker-in-docker-with-tls-enabled-in-the-docker-executor
    DOCKER_HOST: tcp://docker:2376

    DOCKER_TLS_VERIFY: "1"
    DOCKER_TLS_CERTDIR: "/certs"
    DOCKER_CERT_PATH: "/certs/client"

    # Faster than the default, apparently
    DOCKER_DRIVER: overlay2

    DOCKER_VERSION: "20.10"

.dagger:
  extends: [.docker]
  variables:
    DAGGER_VERSION: 0.2.232
    DAGGER_LOG_FORMAT: plain
    DAGGER_CACHE_PATH: .dagger-cache

    ARGS: ""
  cache:
    key: dagger-${CI_JOB_NAME}
    paths:
      - ${DAGGER_CACHE_PATH}
  before_script:
    - |
      # install dagger
      cd /usr/local
      wget -O - https://dl.dagger.io/dagger-cue/install.sh | VERSION="$DAGGER_VERSION" sh
      cd -

      dagger-cue version
  script:
    - dagger-cue project update
    - |
      dagger-cue \
          do \
          --cache-from type=local,src=${DAGGER_CACHE_PATH} \
          --cache-to type=local,mode=max,dest=${DAGGER_CACHE_PATH} \
          ${ARGS}

build:
  extends: [.dagger]
  variables:
    ARGS: build
```

:::caution
`Gitlab`'s template above is using a `Docker-in-Docker` service. No UNIX socket (`/var/run/docker.sock`) will be available on the host unless you specifically mount it.

The recommended way of interacting with the docker daemon using the `universe.dagger.io/docker/cli` package is to rely on the TCP connection:

```cue
package example

import (
  "dagger.io/dagger"

  "universe.dagger.io/docker/cli"
)

dagger.#Plan & {
  client: {
    filesystem: "/certs/client": read: contents: dagger.#FS
    env: DOCKER_PORT_2376_TCP: string
  }
  actions: {
    test: cli.#Run & {
      host:   client.env.DOCKER_PORT_2376_TCP
      always: true
      certs: client.filesystem."/certs/client".read.contents
      command: {
        name: "docker"
        args: ["info"]
      }
    }
  }
}
```

:::

</TabItem>

<TabItem value="jenkins">

<iframe width="800" height="450" style={{width: '100%', marginBottom: '2rem'}} src="https://youtube.com/embed/7u2A4etUuRY" title="YouTube video player" frameborder="0" allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; fullscreen"></iframe>

With `docker` client and `dagger-cue` installed on your Jenkins agent, a Docker host available (can be `docker:dind`), and agents labeled in Jenkins with `dagger-cue`:

```groovy title="Jenkinsfile"
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
            dagger-cue do hello --log-format=plain
        '''
      }
    }
  }
}
```

</TabItem>

<TabItem value="tekton">

```yaml title="tekton/tasks/todo-app.yaml"
apiVersion: tekton.dev/v1beta1
kind: Pipeline
metadata:
  name: dagger-cue
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
      runAfter: ["fetch-repo"] # Wait until the clone is done before deploying the app.
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

              dagger-cue version
              dagger-cue do $(params.dagger-action)

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
      value: https://github.com/dagger/todoapp.git
    - name: netlify-site-name
      value: todoapp-dagger-europa
    - name: netlify-team
      value: dagger
```

</TabItem>

<TabItem value="azure-pipelines">

Azure Pipelines do not currently support a native task; however, it is still possible to run Dagger on Mac, Linux or Windows hosted agent.

To use Dagger on Mac or Linux hosted agent you can use the following pipeline file.

```yaml file=../tests/getting-started/azure-pipelines.yml title="azure-pipelines.yml"

```

Since you cannot use the `install.sh` script on a Windows hosted agent, you will need to update the install task to:

```yaml
- task: ChocolateyCommand@0
  inputs:
    command: "install"
    installPackageId: "dagger-cue"
    installPackageVersion: "$(DAGGER_VERSION)"
  displayName: Install Dagger $(DAGGER_VERSION)
```

</TabItem>
</Tabs>

## Conclusion

This tutorial introduced you to the Dagger CUE SDK. It explained how to install the SDK and use it to build, test and deploy an application locally and remotely. It also provided code samples for common CI environments.

Use the [Guides](../232322-guides.md) and [SDK Reference](../284903-reference.md) to learn more about the Dagger CUE SDK.
