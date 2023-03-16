---
slug: /899944/aws-cdk-ecs
displayed_sidebar: "current"
category: "guides"
tags: ["go", "aws", "cdk", "ecs", "fargate"]
authors: ["Sam Alba"]
date: "2023-03-16"
---

# Use Dagger with the AWS Cloud Development Kit (CDK)

:::note
View a [demo of using Dagger with the AWS CDK](https://youtu.be/ESZKu8VWSGA).
:::

## Introduction

The [AWS Cloud Development Kit (CDK)](https://docs.aws.amazon.com/cdk/v2/guide/home.html) is a framework that enables developers to use their programming language of choice to describe infrastructure resources on AWS.

Although the CDK provides several helpers to facilitate building applications, this tutorial demonstrates how to delegate all the CI tasks (building the application, running tests, etc.) to a Dagger pipeline that integrates with the CDK to manage the infrastructure resources.

You will learn how to:

- Configure the AWS CDK
- Provision a container image repository on [Amazon Elastic Container Registry (ECR)](https://aws.amazon.com/ecr/)
- Provision a cluster on [Amazon Elastic Container Service (ECS)](https://aws.amazon.com/ecs/) using [AWS Fargate](https://aws.amazon.com/fargate/)
- Build, test and deploy an application to the AWS ECS cluster
- Run all of the above through a Dagger pipeline

:::tip
The concepts demonstrated in this tutorial can be applied to any other Infrastructure as Code (IaC) tool. The code example shown below can also be reused to provision another infrastructure stack (such as Amazon EKS, AWS Lambda and others). Reusing the code example for your own needs is covered in Appendix A.
:::

## Requirements

This tutorial assumes that:

- You have a basic understanding of the [Go programming language](https://go.dev/).
- You have a basic understanding of [AWS CloudFormation](https://aws.amazon.com/cloudformation/getting-started/).
- You have access to a [AWS IAM user with permissions to create new resources on an AWS region](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_users_create.html#id_users_create_console).
- Your [AWS IAM user has access keys](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_access-keys.html#Using_CreateAccessKey) and those keys are [configured to be used from your local host](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-quickstart.html).
- You have [installed the AWS CDK CLI](https://docs.aws.amazon.com/cdk/v2/guide/cli.html) on your local host (only required for bootstrapping the CDK for a specific region).

## Step 1: Bootstrap the AWS CDK

The AWS CDK stores its state in an AWS CloudFormation stack named `CDKToolkit`. This stack needs to be present on every AWS region where you are managing resources using the AWS CDK.

In order to bootstrap the AWS CDK for a region, simply run the following command from a terminal. replace the `AWS-ACCOUNT-NUMBER` and `AWS-REGION` placeholders with the corresponding AWS account details.

```shell
cdk bootstrap AWS-ACCOUNT-NUMBER/AWS-REGION
```

More information regarding this step is available in the [AWS CDK CLI documentation](https://docs.aws.amazon.com/cdk/v2/guide/cli.html#cli-bootstrap).

## Step 2: Create the Dagger pipeline

The example application used in this tutorial is a simple React application. Once the AWS CDK is bootstrapped, the next step is to create a Dagger pipeline to build, publish and deploy this example application.

Obtain the Dagger pipeline code and its related helper functions from GitHub, as below:

```shell
git clone https://github.com/dagger/examples.git
cd ./go/aws-cdk
```

This code is organized as follows:

- `main.go`: This file contains the Dagger pipeline that builds the application, builds the container image of the application, publishes it and calls the AWS CDK to interface with the AWS infrastructure.
- `aws.go`: This file contains helper functions for use with the AWS CDK CLI and the AWS API.
- `registry.go`: This file contains helper functions to initialize the AWS ECR registry.
- `infra/`: This subdirectory contains all the code related to the AWS CDK stacks. It is a standalone AWS CDK project that can be used directly from the AWS CDK CLI. It describes two AWS CDK stacks: one for the AWS ECR registry and one for the AWS ECS/Fargate cluster.

This `main.go` file contains three functions:

- The `main()` function creates a Dagger client and an AWS client, initializes an AWS ECR container registry and invokes the `build()` and `deployToEcs()` functions in sequence.
- The `build()` function obtains the application source code, runs the tests, builds a container image of the application and publishes the image to the AWS ECR registry.
- The `deployToEcs()` function deploys the built container image to the AWS ECS cluster.

```go file=./snippets/aws-cdk-ecs/main.go
```

The `build()` function is the main workhorse here, so let's step through it in detail:

- It uses the Dagger client's `CacheVolume()` method to initialize a new cache volume.
- It uses the client's `Git()` method to query the Git repository for the example application. This method returns a `GitRepository` object.
- It uses the `GitRepository` object's `Commit()` method to obtain a reference to the repository tree at a specific commit and then uses the resulting `GitRef` object's `Tree()` and `Directory()` methods to retrieve the filesystem tree and source code directory root.
- It uses the client's `Container().From()` method to initialize a new container from a Node.js base image. The `From()` method returns a new `Container` object with the result.
- It uses the `Container.WithMountedDirectory()` method to mount the source code directory on the host at the `/src` mount point in the container and the `Container.WithMountedCache()` method to mount the cache volume at the `/src/node_modules/` mount point in the container.
- It uses the `Container.WithWorkdir()` method to set the working directory to the `/src` mount point.
- It uses the `Container.WithExec()` method to define the `npm install` command. When executed, this command downloads and installs dependencies in the `node_modules/` directory. Since this directory is defined as a cache volume, its contents will persist even after the pipeline terminates and can be reused on the next pipeline run.
- It chains additional `WithExec()` method calls to run tests and build the application. The build result is stored in the `./build` directory in the container and a reference to this directory is saved in the `buildDir` variable.
- It creates a file containing the AWS ECR password and stores a reference to it as a secret using the `Secret()` method.
- It uses the `Container.WithDirectory()` method to initialize a new `nginx` container and transfer the filesystem state saved in the `buildDir` variable (the built application) to the container at the path `/usr/share/nginx/html`. The result is a container image with the built application in the NGINX webserver root directory.
- It then uses the `WithRegistryAuth()` and `Publish()` methods to publish the final container image to AWS ECR.

## Step 3: Test the Dagger pipeline locally

To build and run the Dagger pipeline from your local host, execute the following commands in a shell, from the `go/aws-cdk` directory. Replace the `AWS-REGION` placeholder with the AWS region you want to use to deploy the ECS cluster. This should be the same region where the CDK was previously bootstrapped (Step 1).

```shell
go build -o pipeline
AWS_REGION="AWS-REGION" ./pipeline
```

The first time the pipeline runs, it takes several minutes to complete because the AWS resources (AWS ECR, AWS VPC, AWS ECS...) need to be fully provisioned.

However, if you re-run it, it completes almost instantly. This is due to the Dagger cache, which knows which step in the pipeline needs to be executed according to what changed from the previous run.

Once the pipeline completes, it displays an HTTP URL. Browse to this URL in your web browser to see the example application running on the newly provisioned AWS ECS cluster.

## Conclusion

This tutorial walked you through the process of integrating the AWS CDK into a Dagger pipeline and building, publishing and deploying an application on AWS infrastructure using Dagger.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go SDK Reference](https://pkg.go.dev/dagger.io/dagger) to learn more about Dagger.

## Appendix A: Repurposing this example for your own needs

The example in this tutorial implements a Dagger pipeline that builds, tests and deploys a simple application on specific infrastructure. It's likely that it will not correspond exactly to the infrastructure or the pipeline steps you need. This section explains how to reuse and adapt the example code to your own needs.

### Replace AWS CDK stacks with other IaC tools

The `infra/` directory is a complete AWS CDK project bootstrapped with the AWS CDK CLI. You can start again from an empty `infra/` directory and run:

```shell
cdk init app --language go
```

At this point, you can specify another programming language supported by the AWS CDK.

:::tip
Given that the AWS CDK stack is deployed from a container via the Dagger pipeline, the language used for the AWS CDK project need not be the same as the language used for the Dagger pipeline. This means that you can - for example - deploy an AWS CDK stack implemented in Java from a Dagger pipeline written in Python.
:::

The same code structure can also be reused to integrate tools like Terraform or Pulumi. Terraform, Pulumi and the AWS CDK share some common structures: a project, a stack, inputs (or parameters) and outputs (among several other concepts that were left out for simplicity). They also provide a CLI to interact with the infrastructure.

As a result, it is quite simple to swap out the AWS CDK CLI with one of the others mentioned above while interfacing with the Dagger pipeline in a similar way (passing inputs to the IaC tool and using outputs from the infrastructure in another pipeline step).

### Reuse AWS CDK helper functions

The code in `aws.go` implements helpers to call the AWS CDK CLI and read stack outputs. These helpers can be reused "as is" in another project using the AWS CDK.
