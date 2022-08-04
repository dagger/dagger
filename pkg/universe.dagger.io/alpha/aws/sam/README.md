# AWS Sam usage

This is a [dagger](https://dagger.io/) package to help you deploy serverless functions with ease. 

## :closed_book: Description

This package is a superset of [AWS SAM](https://docs.aws.amazon.com/serverless-application-model/latest/developerguide/what-is-sam.html), which allows you to build and deploy Lambda function(s). <br>


The aim is to integrate the lambda deployment to your current [dagger](https://dagger.io/) pipeline. This way, you can __build__ and __deploy__ with a single [dagger environment](https://docs.dagger.io/1200/local-dev/).

## :hammer_and_pick: Prerequisite 

Before we can build, test & deploy our example app with dagger, we need to have Docker Engine running.
You also need a dagger installation on your machine. If you don't have one, you can find the required steps [here](https://docs.dagger.io/install).

## :beginner: Quickstart

Everyone should be able to develop and deploy their sam functions using a local pipeline. Having to commit & push in order to test a change slows down iteration. 

### Build & deploy locally

For a sam project you have to provide the following environment variables: 
```text
    AWS_ACCESS_KEY_ID=<your AWS access key id>
    AWS_REGION=<your AWS region>
    
    // if you use a .zip archive you have to provide a S3 bucket
    AWS_S3_BUCKET=<your S3 bucket>

    AWS_SECRET_KEY=<your AWS secret key>
    AWS_STACK_NAME=<your stack name>
```

Then you are ready to write your plan to build and deploy a sam function with dagger. 

#### Plan for a .zip archive

This is a the plan for a `.zip archives` function. 

```cue title="samZip.cue"
package myAwesomeSamPackage

import (
    "dagger.io/dagger"
    "universe.dagger.io/alpha/aws/sam"
)

dagger.#Plan & {
    _common: {
        config: sam.#Config & {
            accessKey: client.env.AWS_ACCESS_KEY_ID
            region: client.env.AWS_REGION
            bucket: client.env.AWS_S3_BUCKET
            secretKey: client.env.AWS_SECRET_ACCESS_KEY
            stackName: client.env.AWS_STACK_NAME
        }
    }

    client: {
        filesystem: {
            "./": {
                read: {
                    contents: dagger.#FS
                }
            }
        }
        env: {
            AWS_ACCESS_KEY_ID: string
            AWS_REGION: string
            AWS_S3_BUCKET: string
            AWS_SECRET_ACCESS_KEY: dagger.#Secret
            AWS_STACK_NAME: string
        }
    }

    actions: {
        build: sam.#Package & _common & {
            fileTree: client.filesystem."./".read.contents
        }
        deploy: sam.#DeployZip & _common & {
            input: build.output
        }
    }
}
```

Now you can run `dagger do deploy` this should build your sam function and deploy everything to AWS Lambda. 

#### Plan for a docker image

This is a the plan for a `docker image` function. 
In case of building a docker image we have to define the docker socket and we don't need the S3 bucket anymore. 

```cue title="samImage.cue"
package myAwesomeSamPackage

import (
    "dagger.io/dagger"
    "universe.dagger.io/alpha/aws/sam"
)

dagger.#Plan & {
    _common: {
        config: sam.#Config & {
            accessKey: client.env.AWS_ACCESS_KEY_ID
            region: client.env.AWS_REGION
            secretKey: client.env.AWS_SECRET_ACCESS_KEY
            stackName: client.env.AWS_STACK_NAME
            clientSocket: client.network."unix:///var/run/docker.sock".connect
        }
    }

    client: {
        filesystem: {
            "./": {
                read: {
                    contents: dagger.#FS
                }
            }
        }
        network: "unix:///var/run/docker.sock": connect: dagger.#Socket
        env: {
            AWS_ACCESS_KEY_ID: string
            AWS_REGION: string
            AWS_SECRET_ACCESS_KEY: dagger.#Secret
            AWS_STACK_NAME: string
        }
    }

    actions: {
        build: sam.#Build & _common & {
            fileTree: client.filesystem."./".read.contents
        }
        deploy: sam.#Deployment & _common & {
            input: build.output
        }
    }
}
```

Now you can run `dagger do deploy` this should build your sam function and deploy everything to AWS Lambda. 

### :zap: Build & deploy with GitLab CI

#### Build & deploy .zip archives with GitLab CI

If you plan to run the above plans in a GitLab CI environment you can do that without changes for the `.zip archives`

For that you have to create a `.gitlab-ci.yml` with the following content: 

```yml
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
        DAGGER_VERSION: 0.2.27
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

    script:
        - dagger project update
        - |
            dagger \
                do \
                ${ARGS} \
                --log-format=plain \
                --log-level debug

build:
    extends: [.dagger]
    variables:
        ARGS: deploy
```

If you trigger the pipeline this should build your sam function and deploy everything to AWS Lambda.

:bulb: Don't forget to set the needed environment variables in your GitLab CI environment. 


#### Build & deploy .zip archives with GitLab CI

If you plan to run the plan with the docker image in a GitLab CI environment you have to make small changes to get everything up and running. This comes because on GitLab you have to use a `DinD-Service` and you can not connect via `docker socket` with it you have to use the `tcp-socket`.

First we have to change the plan itself to use `tcp-socket` if it is executed in the GitLab environment. 

```cue title="samImage.cue"
package myAwesomeSamPackage

import (
    "dagger.io/dagger"
    "universe.dagger.io/alpha/aws/sam"
)

dagger.#Plan & {
    _common: {
        config: sam.#Config & {
            ciKey: actions.ciKey
            accessKey: client.env.AWS_ACCESS_KEY_ID
            region: client.env.AWS_REGION
            secretKey: client.env.AWS_SECRET_ACCESS_KEY
            stackName: client.env.AWS_STACK_NAME
            if (client.env.DOCKER_PORT_2376_TCP != _|_) {
                host: client.env.DOCKER_PORT_2376_TCP
            }
            if (actions.ciKey != null) {
                certs: client.filesystem."/certs/client".read.contents
            }
            clientSocket: client.network."unix:///var/run/docker.sock".connect
        }
    }

    client: {
        filesystem: {
            "./": {
                read: {
                    contents: dagger.#FS
                }
            }
            if actions.ciKey != null {
                "/certs/client": read: contents: dagger.#FS
            }
        }

        if actions.ciKey == null {
            network: "unix:///var/run/docker.sock": connect: dagger.#Socket
        }
        
        env: {
            AWS_ACCESS_KEY_ID: string
            AWS_REGION: string
            AWS_SECRET_ACCESS_KEY: dagger.#Secret
            AWS_STACK_NAME: string
            DOCKER_PORT_2376_TCP?: string
        }
    }

    actions: {
        ciKey: *null | string
        build: sam.#Build & _common & {
            fileTree: client.filesystem."./".read.contents
        }
        deploy: sam.#Deployment & _common & {
            input: build.output
        }
    }
}
```

Now we have to create our `.gitlab-ci.yml` with the following content: 

```yml
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
        DAGGER_VERSION: 0.2.27
        DAGGER_LOG_FORMAT: plain
        DAGGER_CACHE_PATH: .dagger-cache
        aws_access_key_id: $aws_access_key_id
        aws_region: $aws_region
        aws_secret_access_key: $aws_secret_access_key
        aws_stack_name: $aws_stack_name
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

    script:
        - dagger project update
        - |
            dagger \
                do \
                ${ARGS} \
                --with 'actions: ciKey: "gitlab"' \
                --log-format=plain \
                --log-level debug

build:
    extends: [.dagger]
    variables:
        ARGS: deploy
```

As you can see we give an argument `--with 'actions: ciKey: "gitlab"'` to the `dagger do deploy` call. 

If you trigger the pipeline this should build your sam function and deploy everything to AWS Lambda.

:bulb: Don't forget to set the needed environment variables in your GitLab CI environment. 

## :handshake: Contributing

If you have a specific need, don't hesitate to write an [issue](https://github.com/munichbughunter/dagger) or you plan to contribute please follow [this really helpful guilde](https://docs.dagger.io/1227/contributing/)! :rocket:

See the workflow below to contribute.

> :bulb: Check that [post](https://chris.beams.io/posts/git-commit/) to learn how write good commit message

## 	:superhero_man: Maintainer(s)

- [Patrick Döring](https://github.com/munichbughunter)
