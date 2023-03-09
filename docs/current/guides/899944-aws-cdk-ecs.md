---
slug: /899944/aws-cdk-ecs
displayed_sidebar: "current"
category: "guides"
tags: ["go", "aws", "cdk", "ecs", "fargate"]
authors: ["Sam Alba"]
date: "2023-03-02"
---

# Use Dagger with the AWS CDK (Cloud Development Kit)

:::note
[Watch a live demo](https://youtu.be/ESZKu8VWSGA) of this tutorial in the Dagger Community Call (23 Feb 2023). For more demos, [join the next Dagger Community Call](https://dagger.io/events).
:::

## Introduction

[The AWS CDK](https://docs.aws.amazon.com/cdk/v2/guide/home.html) is a framework that enables developers to use their programming language of choice to describe Infrastructure resources on AWS. However there are several things that the CDK will not support, for example: building your application, running your tests, manage your container images, etc... This is where Dagger comes in handy.

This tutorial teaches you how to integrate the AWS Cloud Development Kit (CDK) in a Dagger pipeline. You will learn how to:

- Configure the AWS CDK
- Provision a container image repository on AWS ECR
- Provision a cluster on AWS ECS using Fargate
- Build, test and deploy an application to the ECS cluster
- Run all of the above from the Dagger pipeline

The same concept can be applied to any other Infrastructure as Code (IaC) tool. The same code can also be reused to provision another infrastructure stack (EKS, Lambda, etc...). Reusing this example for your own needs is covered in Step 4.

## Requirements

This tutorial assumes that:

- You have a basic understanding of the [Go programming language](https://go.dev/).
- You have a basic understanding of [AWS CloudFormation](https://aws.amazon.com/cloudformation/getting-started/).
- You have access to a [AWS IAM user with permissions to create new resources on an AWS region](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_users_create.html#id_users_create_console).
- Your [IAM user has access keys](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_access-keys.html#Using_CreateAccessKey) and [they are configured to be used from local machine](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-quickstart.html).
- [You installed the CDK CLI on your local machine](https://docs.aws.amazon.com/cdk/v2/guide/cli.html) (only required for bootstrapping the CDK on a region).

## Step 1: Boostrapping the AWS CDK

The AWS CDK stores its state entirely on AWS, starting with some meta-data related to the version of the CDK you are using, in a CloudFormation stack named `CDKToolkit`. This stack needs to be present on every AWS region where you are managing resources using the CDK.

In order to bootstrap the CDK on a region, simply run the following command from a terminal:

```shell
cdk bootstrap ACCOUNT-NUMBER/REGION
```

_(Example: `cdk bootstrap 1111111111/us-east-1`)_

In case you need more information regarding this step: [read the CLI document page](https://docs.aws.amazon.com/cdk/v2/guide/cli.html#cli-bootstrap).

## Step 2: pull the example code

```shell
git clone https://github.com/dagger/examples.git
cd ./go/aws-cdk
```

Here is how this code is organized:

- `main.go`: contains the Dagger pipeline that build the app, build the container image, publishes it and calls the CDK for interfacing with the infrastructure
- `aws.go`: implements some helper functions to call the CDK CLI and the AWS API.
- `infra/` is a subdirectory that contains all the CDK Stacks code. It's a standalone CDK project that can be used directly from the CDK CLI. It describes two CDK Stacks: one for the ECR Registry, one for the ECS/Fargate cluster.

## Step 3: run the Dagger pipeline locally

To build and run the pipeline, execute the following commands in a shell, from the `./go/aws-cdk` directory:

```shell
go build -o pipeline
AWS_REGION="XXX" ./pipeline
```

Here, replace `XXX` with the AWS region you want to use to deploy the ECS cluster. Note that this needs to be a region where the CDK was previously bootstrapped (see "Step 1").

The first time the pipeline runs, it will take several minutes to complete because the AWS Resources (ECR repository, VPC, ECS cluster, etc...) will need to be fully provisioned.

However if you re-run it a second time, you will see that it will complete almost instantly. This is due to the Dagger cache that knows what step in the pipeline needs to be executed according to what changed from the previous run.

Once the pipeline completes, you will see an HTTP URL at the end. If you open it in your web browser, you will see the example app up and running from the newly provisioned ECS cluster.

## Step 4: how to reuse this example for your own needs

The example above implements a Dagger pipeline that builds, tests and deploys a very application code on a specific provisioned infrastructure. It's likely that it will not correspond exactly to the infrastructure or the pipeline steps you need. This step explains what part of the example you can reuse and adapt to your own needs.

### Replacing the CDK Stacks with your own infrastructure

The `./infra` directory is a complete CDK project bootstrap with the CDK CLI. You can start again from an empty `infra` directory and run:

```shell
cdk init app --language go
```

:::note
This is where you can specify another programming language supported by the CDK, given that the CDK Stack will be deployed from a container from the Dagger pipeline, the language does not have to map the language of the Dagger SDK (e.g.: you can deploy a CDK Stack implemented in Java from a Dagger pipeline written in Python).
:::

### What about other IaC tools?

The same code structure can also be reused to integrate tools like Terraform or Pulumi. Terraform, Pulumi and the AWS CDK share some common structure: a project, a stack, inputs (or parameters) and outputs (among several other concepts that were left out for simplicity). They also provide a CLI to interact with the infrastructure.

As a result, it would be simple to swap out the CDK CLI with another one mentioned above while interfacing with the Dagger pipeline similarly (passing inputs to the IaC tool and using outputs from the infrastructure in another pipeline step).

### Other reusable components

The code in `aws.go` implements helpers to call the CDK CLI and read Stacks outputs. They can be reused as-is in another project using the AWS CDK.

## Conclusion

This tutorial walked you through integrating the AWS CDK into a Dagger pipeline and it gave some guidance to reuse the same example with your own CDK Stack.

Use the [API Key Concepts](../api/975146-concepts.md) page and the [Go SDK Reference](https://pkg.go.dev/dagger.io/dagger) to learn more about Dagger.
