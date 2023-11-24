---
slug: /183109/aws-codebuild-codepipeline
displayed_sidebar: "current"
category: "guides"
tags: ["nodejs", "go", "python", "aws-codepipelines", "aws-codebuild", "aws"]
authors: ["Vikram Vaswani"]
date: "2023-06-13"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Use Dagger with AWS CodeBuild and AWS CodePipeline

## Introduction

This tutorial teaches you how to use Dagger to continuously build and publish a Node.js application with AWS CodePipeline. You will learn how to:

- Create an AWS CodeBuild project and connect it to an AWS CodeCommit repository
- Create a Dagger pipeline using a Dagger SDK
- Integrate the Dagger pipeline with AWS CodePipeline to automatically build and publish the application on every repository commit

## Requirements

This tutorial assumes that:

- You have a basic understanding of the JavaScript programming language.
- You have a basic understanding of the AWS CodeCommit, AWS CodeBuild and AWS CodePipeline service. If not, learn about [AWS CodeCommit](https://docs.aws.amazon.com/codecommit/latest/userguide/welcome.html), [AWS CodeBuild](https://docs.aws.amazon.com/codebuild/latest/userguide/welcome.html) and [AWS CodePipeline](https://docs.aws.amazon.com/codepipeline/latest/userguide/welcome.html).
- You have a Go, Python or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have the Dagger CLI installed in your development environment. If not, [install the Dagger CLI](../cli/465058-install.md).
- You have an account with a container registry, such as Docker Hub, and privileges to push images to it. If not, [register for a free Docker Hub account](https://hub.docker.com/signup).
- You have an AWS account with appropriate privileges to create and manage AWS CodeBuild and AWS CodePipeline resources. If not, [register for an AWS account](https://aws.amazon.com/).
- You have an AWS CodeCommit repository containing a Node.js Web application. This repository should also be cloned locally in your development environment. If not, follow the steps in Appendix A to [create and populate a local and AWS CodeCommit repository with an example Express application](#appendix-a-create-an-aws-codecommit-repository-with-an-example-express-application).

:::tip
This guide uses AWS CodeCommit as the source provider, but AWS CodeBuild also supports GitHub, GitHub Enterprise, BitBucket and Amazon S3 as source providers.
:::

## Step 1: Create an AWS CodeBuild project

The first step is to create an AWS CodeBuild project, as described below.

1. Log in to the [AWS console](https://console.aws.amazon.com).
1. Navigate to the "CodeBuild" section.
1. Navigate to the "Build projects" page.
1. Click "Create build project".
1. On the "Create build project" page, input the following details, adjusting them as required for your project:
    - In the "Project configuration" section:
        - Project name: `myapp-codebuild-project`
    - In the "Source" section:
        - Source: `AWS CodeCommit`
        - Reference type: `Branch`
        - Branch: `main`
    - In the "Environment" section:
        - Environment image: `Managed image`
        - Operating system: `Amazon Linux 2`
        - Runtime(s): `Standard`
        - Image: `aws/codebuild/amazonlinux2-x86_64-standard:5.0` (or latest available for your architecture)
        - Image version: `Always use the latest image for this runtime version`
        - Environment type: `Linux`
        - Privileged: Enabled
        - Service role: `New service role`
        - Environment variables:
          - `REGISTRY_ADDRESS`: Your registry address (`docker.io` for Docker Hub)
          - `REGISTRY_USERNAME`: Your registry username
          - `REGISTRY_PASSWORD`: Your registry password
    - In the "Buildspec" section:
        - Build specifications: `Use a buildspec file`
    - In the "Artifacts" section:
        - Type: `No artifacts`
    - In the "Logs" section:
        - CloudWatch logs: `Enabled`
1. Click "Create build project".

AWS CodeBuild creates a new build project.

The following images visually illustrate the AWS CodeBuild project configuration:

![Create CodeBuild project - project](/img/current/guides/aws-codebuild-codepipeline/codebuild-project.png)

![Create CodeBuild project - source](/img/current/guides/aws-codebuild-codepipeline/codebuild-source.png)

![Create CodeBuild project - image](/img/current/guides/aws-codebuild-codepipeline/codebuild-image.png)

![Create CodeBuild project - environment](/img/current/guides/aws-codebuild-codepipeline/codebuild-env.png)

![Create CodeBuild project - buildspec](/img/current/guides/aws-codebuild-codepipeline/codebuild-spec.png)

## Step 2: Create the Dagger pipeline

The next step is to create a Dagger pipeline to build a container image of the application and publish it to the registry.

<Tabs groupId="language">
<TabItem value="Go">

1. In the application directory, install the Dagger SDK:

  ```shell
  go mod init main
  go get dagger.io/dagger@latest
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.go` and add the following code to it.

  ```go file=./snippets/aws-codebuild-codepipeline/main.go
  ```

  This file performs the following operations:
    - It imports the Dagger SDK.
    - It checks for registry credentials in the host environment.
    - It creates a Dagger client with `Connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `SetSecret()` method to set the registry password as a secret for the Dagger pipeline.
    - It uses the client's `Host().Directory()` method to obtain a reference to the current directory on the host, excluding the `node_modules` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `Container().From()` method to initialize a new container image from a base image. The additional `platform` argument to the `Container()` method instructs Dagger to build for a specific architecture. In this example, the base image is the `node:18` image and the architecture is `linux/amd64`. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `WithDirectory()` method to return the container image with the host directory written at the `/src` path, and the `WithWorkdir()` method to set the working directory in the container image.
    - It chains the `WithExec()` method again to install dependencies with `npm install`, build a production image of the application with `npm run build`, and set the default entrypoint argument to `npm start` using the `WithDefaultArgs()` method.
    - It uses the `WithRegistryAuth()` method to authenticate the Dagger pipeline against the registry using the credentials from the host environment (including the password set as a secret previously)
    - It invokes the `Publish()` method to publish the container image to the registry. It also prints the SHA identifier of the published image.

1. Run the following command to update `go.sum`:

  ```shell
  go mod tidy
  ```

</TabItem>
<TabItem value="Node.js">

1. In the application directory, install the Dagger SDK:

  ```shell
  npm install @dagger.io/dagger@latest--save-dev
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `index.mjs` and add the following code to it.

  ```javascript file=./snippets/aws-codebuild-codepipeline/index.mjs
  ```

  This file performs the following operations:
    - It imports the Dagger SDK.
    - It checks for registry credentials in the host environment.
    - It creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `setSecret()` method to set the registry password as a secret for the Dagger pipeline.
    - It uses the client's `host().directory()` method to obtain a reference to the current directory on the host, excluding the `node_modules` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `container().from()` method to initialize a new container image from a base image. The additional `platform` argument to the `container()` method instructs Dagger to build for a specific architecture. In this example, the base image is the `node:18` image and the architecture is `linux/amd64`. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `withDirectory()` method to return the container image with the host directory written at the `/src` path, and the `withWorkdir()` method to set the working directory in the container image.
    - It chains the `withExec()` method again to install dependencies with `npm install`, build a production image of the application with `npm run build`, and set the default entrypoint argument to `npm start` using the `withDefaultArgs()` method.
    - It uses the `withRegistryAuth()` method to authenticate the Dagger pipeline against the registry using the credentials from the host environment (including the password set as a secret previously)
    - It invokes the `publish()` method to publish the container image to the registry. It also prints the SHA identifier of the published image.

</TabItem>
<TabItem value="Python">

1. In the application directory, create a virtual environment and install the Dagger SDK:

  ```shell
  pip install dagger-io
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.py` and add the following code to it.

  ```python file=./snippets/aws-codebuild-codepipeline/main.py
  ```

  This file performs the following operations:
    - It imports the Dagger SDK.
    - It checks for registry credentials in the host environment.
    - It creates a Dagger client with `dagger.Connection()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `set_secret()` method to set the registry password as a secret for the Dagger pipeline.
    - It uses the client's `host().directory()` method to obtain a reference to the current directory on the host, excluding the `node_modules` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `container().from_()` method to initialize a new container image from a base image. The additional `platform` argument to the `container()` method instructs Dagger to build for a specific architecture. In this example, the base image is the `node:18` image and the architecture is `linux/amd64`. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `with_directory()` method to mount the host directory into the container image at the `/src` mount point, and the `with_workdir()` method to set the working directory in the container image.
    - It chains the `with_exec()` method again to install dependencies with `npm install`, build a production image of the application with `npm run build`, and set the default entrypoint argument to `npm start` using the `with_default_args()` method.
    - It uses the `with_registry_auth()` method to authenticate the Dagger pipeline against the registry using the credentials from the host environment (including the password set as a secret previously)
    - It invokes the `publish()` method to publish the container image to the registry. It also prints the SHA identifier of the published image.

</TabItem>
</Tabs>

:::tip
Most `Container` object methods return a revised `Container` object representing the new state of the container. This makes it easy to chain methods together. Dagger evaluates pipelines "lazily", so the chained operations are only executed when required - in this case, when the container is published. Learn more about [lazy evaluation in Dagger](../api/975146-concepts.mdx#lazy-evaluation).
:::

## Step 3: Add the build specification file

AWS CodeBuild relies on a [build specification file](https://docs.aws.amazon.com/codebuild/latest/userguide/build-spec-ref.html) to execute the build. This build specification file defines the stages of the build, and the commands to be run in each stage.

1. In the application directory, create a new file at `buildspec.yml` with the following content:

  <Tabs groupId="language">
  <TabItem value="Go">

  ```yaml file=./snippets/aws-codebuild-codepipeline/buildspec-go.yml
  ```

  </TabItem>
  <TabItem value="Node.js">

  ```yaml file=./snippets/aws-codebuild-codepipeline/buildspec-nodejs.yml
  ```

  </TabItem>
  <TabItem value="Python">

  ```yaml file=./snippets/aws-codebuild-codepipeline/buildspec-python.yml
  ```

  </TabItem>
  </Tabs>

  This build specification defines four steps, as below:
    - The first step installs the Dagger SDK on the CI runner.
    - The second step installs the Dagger CLI on the CI runner.
    - The third step executes the Dagger pipeline.
    - The fourth step displays a message with the date and time of build completion.

1. Commit the Dagger pipeline and build specification file to the repository:

  ```shell
  git add buildspec.yml
  git add ci/*
  git commit -a -m "Added Dagger pipeline and build specification"
  git push
  ```

## Step 4: Create an AWS CodePipeline for Dagger

The final step is to create an AWS CodePipeline to run the Dagger pipeline whenever the source repository changes, as described below.

1. Log in to the [AWS console](https://console.aws.amazon.com).
1. Navigate to the "CodePipeline" section.
1. Navigate to the "Pipelines" page.
1. Click "Create pipeline".
1. On the "Create new pipeline" sequence of pages, input the following details, adjusting them as required for your project:
    - In the "Pipeline settings" section:
        - Pipeline name: `myapp-pipeline`
        - Service role: `New service role`
    - In the "Source" section:
        - Source provider: `AWS CodeCommit`
        - Repository name: `myapp`
        - Branch name: `main`
        - Change detection options: `Amazon CloudWatch Events`
        - Output artifact format: `CodePipeline default`
    - In the "Build" section:
        - Build provider: `AWS CodeBuild`
        - Region: Set value to your region
        - Project name: `myapp-codebuild-project`
        - Build type: `Single build`
    - In the "Deploy" section:
        - Click the `Skip deploy stage` button
1. On the "Review" page, review the inputs and click "Create pipeline".

AWS CodePipeline creates a new pipeline.

The following image visually illustrates the AWS CodePipeline configuration:

![Create CodePipeline](/img/current/guides/aws-codebuild-codepipeline/codepipeline.png)

:::info
Environment variables defined as part of the AWS CodeBuild project configuration are available to AWS CodePipeline as well.
:::

## Step 5: Test the Dagger pipeline

Test the Dagger pipeline by committing a change to the repository.

If you are using the example application described in [Appendix A](#appendix-a-create-an-aws-codecommit-repository-with-an-example-express-application), the following commands modify and commit a change to the application's index page:

```shell
git pull
echo -e "export default function Hello() {\n  return <h1>Hello from Dagger on AWS</h1>;\n }" > src/pages/index.js
git add src/pages/index.js
git commit -m "Update index page"
git push
```

The commit triggers the AWS CodePipeline defined in Step 4. The AWS CodePipeline runs the various steps of the job, including the Dagger pipeline script. At the end of the process, the built container is published to the registry and a message similar to the one below appears in the AWS CodePipeline logs:

```shell
Published image to: .../myapp@sha256...
```

Test the published image by executing the commands below (replace the `IMAGE-ADDRESS` placeholder with the address of the published image):

```shell
docker run --rm -p 3000:3000 --name myapp IMAGE-ADDRESS
```

Browse to `http://localhost:3000` to see the application running. If you deployed the example application with the modification above, you see the following output:

```shell
Hello from Dagger on AWS
```

:::tip
Pipelines that pull public images from Docker Hub may occasionally fail with the error "You have reached your pull rate limit. You may increase the limit by authenticating and upgrading...". This error occurs due to [Docker Hub's rate limits](https://www.docker.com/increase-rate-limits/). You can resolve this error by adding explicit Docker Hub authentication as the first step in your build specification file, or by copying public images to your own private registry and pulling from there instead. More information is available in this [Amazon blog post providing advice related to Docker Hub rate limits](https://aws.amazon.com/blogs/containers/advice-for-customers-dealing-with-docker-hub-rate-limits-and-a-coming-soon-announcement/).
:::

## Conclusion

This tutorial walked you through the process of creating a Dagger pipeline to continuously build and publish a Node.js application using AWS services such as AWS CodeBuild and AWS CodePipeline. It used the Dagger SDKs and explained key concepts, objects and methods available in the SDKs to construct a Dagger pipeline. It also demonstrated the process of integrating the Dagger pipeline with AWS CodePipeline to automatically monitor changes to your source repository and trigger new builds in response.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.

## Appendix A: Create an AWS CodeCommit repository with an example Next.js application

This tutorial assumes that you have an AWS CodeCommit repository with a Node.js Web application. If not, follow the steps below to create an AWS CodeCommit repository and commit an example Next.js application to it.

1. Create a directory for the Next.js application:

  ```shell
  mkdir myapp
  cd myapp
  ```

1. Create a skeleton Express application:

  ```shell
  npx create-next-app --js --src-dir --eslint --no-tailwind --no-app --import-alias "@/*" .
  ```

1. Initialize a local Git repository for the application:

  ```shell
  git init
  ```

1. Add a `.gitignore` file and commit the application code:

  ```shell
  echo node_modules >> .gitignore
  git add .
  git commit -a -m "Initial commit"
  ```

1. Log in to the [AWS console](https://console.aws.amazon.com/) and perform the following steps:
    - [Create a new AWS CodeCommit repository](https://docs.aws.amazon.com/codecommit/latest/userguide/how-to-create-repository.html).
    - [Configure SSH authentication](https://docs.aws.amazon.com/codecommit/latest/userguide/setting-up-without-cli.html) for the AWS CodeCommit repository.
    - [Obtain the SSH clone URL](https://docs.aws.amazon.com/codecommit/latest/userguide/how-to-view-repository-details.html#how-to-view-repository-details-console) for the AWS CodeCommit repository.

1. Add the AWS CodeCommit repository as a remote and push the application code to it. Replace the `SSH-URL` placeholder with the SSH clone URL for the repository.

  ```shell
  git remote add origin SSH-URL
  git push -u origin --all
  ```
