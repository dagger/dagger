---
slug: /183109/aws-codebuild-codepipeline
displayed_sidebar: "current"
category: "guides"
tags: ["nodejs", "go", "python", "aws-codepipelines", "aws-codebuild", "aws"]
authors: ["Vikram Vaswani"]
date: "2023-05-30"
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
      - Source reference or Branch: `main`
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
        - `REGISTRY_ADDRESS`: `docker.io` (or your container registry address)
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

## Step 2: Create the Dagger pipeline

The next step is to create a Dagger pipeline to build a container image of the application and publish it to the registry.

<Tabs groupId="language">
<TabItem value="Go">

1. In the application directory, install the Dagger SDK:

  ```shell
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
    - It uses the previous `Container` object's `WithDirectory()` method to mount the host directory into the container image at the `/src` mount point, and the `WithWorkdir()` method to set the working directory in the container image.
    - It chains the `WithExec()` method again to install dependencies with `npm install`, build a production image of the application with `npm run build`, and set the container entrypoint to `npm start` using the `WithEntrypoint()` method.
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
    - It uses the previous `Container` object's `withDirectory()` method to mount the host directory into the container image at the `/src` mount point, and the `withWorkdir()` method to set the working directory in the container image.
    - It chains the `withExec()` method again to install dependencies with `npm install`, build a production image of the application with `npm run build`, and set the container entrypoint to `npm start` using the `withEntrypoint()` method.
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
    - It chains the `with_exec()` method again to install dependencies with `npm install`, build a production image of the application with `npm run build`, and set the container entrypoint to `npm start` using the `with_entrypoint()` method.
    - It uses the `with_registry_auth()` method to authenticate the Dagger pipeline against the registry using the credentials from the host environment (including the password set as a secret previously)
    - It invokes the `publish()` method to publish the container image to the registry. It also prints the SHA identifier of the published image.


</TabItem>
</Tabs>

:::tip
Most `Container` object methods return a revised `Container` object representing the new state of the container. This makes it easy to chain methods together. Dagger evaluates pipelines "lazily", so the chained operations are only executed when required - in this case, when the container is published. Learn more about [lazy evaluation in Dagger](../api/975146-concepts.mdx#lazy-evaluation).
:::


## Step 3: Add the build specification file

## Step 4: Create an AWS CodePipeline for Dagger

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

## Step 5: Test the Dagger pipeline





## Step 4: Add the build specification file

Configure credentials for the Docker Hub registry and the Azure SDK on the local host by executing the commands below, replacing the placeholders as follows:

- Replace the `TENANT-ID`, `CLIENT-ID` and `CLIENT-SECRET` placeholders with the service principal credentials obtained at the end of Step 1.
- Replace the `SUBSCRIPTION-ID` placeholder with your Azure subscription ID.
- Replace the `USERNAME` and `PASSWORD` placeholders with your Docker Hub username and password respectively.

```shell
export AZURE_TENANT_ID=TENANT-ID
export AZURE_CLIENT_ID=CLIENT-ID
export AZURE_CLIENT_SECRET=CLIENT-SECRET
export AZURE_SUBSCRIPTION_ID=SUBSCRIPTION-ID
export DOCKERHUB_USERNAME=USERNAME
export DOCKERHUB_PASSWORD=PASSWORD
```

Once credentials are configured, test the Dagger pipeline by running the command below:

<Tabs groupId="language">
<TabItem value="Go">

```shell
go run ci/main.go
```

</TabItem>
<TabItem value="Node.js">

```shell
node ci/index.mjs
```

</TabItem>
<TabItem value="Python">

```shell
python ci/main.py
```

</TabItem>
</Tabs>

Dagger performs the operations defined in the pipeline script, logging each operation to the console. At the end of the process, the built container is deployed to Azure Container Instances and a message similar to the one below appears in the console output:

  ```shell
  Deployment for image docker.io/.../my-app@sha256... now available at ...
  ```

Browse to the URL shown in the deployment message to see the running application.

If you deployed the example application from [Appendix A](#appendix-a-create-an-azure-devops-repository-with-an-example-express-application), you should see a page similar to that shown below:

![Result of running pipeline from local host](/img/current/guides/azure-pipelines-container-instances/local-deployment.png)

## Step 5: Test the Dagger pipeline

Test the Dagger pipeline by committing a change to the repository.

If you are using the example application described in [Appendix A](#appendix-a-create-an-azure-devops-repository-with-an-example-express-application), the following commands modify and commit a simple change to the application's index page:

```shell
git pull
sed -i 's/Dagger/Dagger on Azure/g' routes/index.js
git add routes/index.js
git commit -m "Update welcome message"
git push
```

The commit triggers the Azure Pipeline defined in Step 5. The Azure Pipeline runs the various steps of the job, including the Dagger pipeline script.

At the end of the process, a new version of the built container image is released to Docker Hub and deployed on Azure Container Instances. A message similar to the one below appears in the Azure Pipelines log:

```shell
Deployment for image docker.io/.../my-app@sha256:... now available at ...
```

Browse to the URL shown in the deployment message to see the running application. If you deployed the example application with the additional modification above, you see a page similar to that shown below:

![Result of running pipeline from Azure Pipelines](/img/current/guides/azure-pipelines-container-instances/azure-pipelines-deployment.png)

## Conclusion

This tutorial walked you through the process of creating a Dagger pipeline to continuously build and deploy a Node.js application on Azure Container Instances. It used the Dagger SDKs and explained key concepts, objects and methods available in the SDKs to construct a Dagger pipeline.

Dagger executes your pipelines entirely as standard OCI containers. This means that pipelines can be tested and debugged locally, and that the same pipeline will run consistently on your local machine, a CI runner, a dedicated server, or any container hosting service. This portability is one of Dagger's key advantages, and this tutorial demonstrated it in action by using the same pipeline on the local host and with Azure Pipelines.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.

## Appendix A: Create an AWS CodeCommit repository with an example Express application

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

1. Add a custom page to the application:

  ```shell
  echo -e "export default function Hello() {\n  return <h1>Hello from Dagger</h1>;\n }" > src/pages/hello.js
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
