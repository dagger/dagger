---
slug: /620941/github-google-cloud
displayed_sidebar: "current"
category: "guides"
tags: ["nodejs", "go", "python", "gitlab-ci", "google-cloud"]
authors: ["Vikram Vaswani"]
date: "2022-12-12"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Use Dagger with GitHub Actions and Google Cloud

:::note
[Watch a live demo](https://youtu.be/-pKmv0VDJBg) of this tutorial in the Dagger Community Call (12 Jan 2023). For more demos, [join the next Dagger Community Call](https://dagger.io/events).
:::

## Introduction

This tutorial teaches you how to use a Dagger pipeline to continuously build and deploy a Node.js application with GitHub Actions on Google Cloud Run. You will learn how to:

- Configure a Google Cloud service account and assign it the correct roles
- Create a Google Cloud Run service accessible at a public URL
- Create a Dagger pipeline using a Dagger SDK
- Run the Dagger pipeline on your local host to manually build and deploy the application on Google Cloud Run
- Use the same Dagger pipeline with GitHub Actions to automatically build and deploy the application on Google Cloud Run on every repository commit

## Requirements

This tutorial assumes that:

- You have a basic understanding of the JavaScript programming language.
- You have a basic understanding of GitHub Actions. If not, [learn about GitHub Actions](https://docs.github.com/en/actions).
- You have a Go, Python or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have the Google Cloud CLI installed. If not, [install the Google Cloud CLI](https://cloud.google.com/sdk/docs/install).
- You have a Google Cloud account and a Google Cloud project with billing enabled. If not, [register for a Google Cloud account](https://cloud.google.com/), [create a Google Cloud project](https://console.cloud.google.com/project) and [enable billing](https://support.google.com/cloud/answer/6293499#enable-billing).
- You have a GitHub account and a GitHub repository containing a Node.js Web application. This repository should also be cloned locally in your development environment. If not, [register for a GitHub account](https://github.com/signup), [install the GitHub CLI](https://github.com/cli/cli#installation) and follow the steps in Appendix A to [create and populate a local and GitHub repository with an example Express application](#appendix-a-create-a-github-repository-with-an-example-express-application).

## Step 1: Create a Google Cloud service account

{@include: ../partials/_google-cloud-service-account-key-setup.md}

## Step 2: Configure Google Cloud APIs and a Google Cloud Run service

{@include: ../partials/_google-cloud-api-run-setup.md}

## Step 3: Create the Dagger pipeline

The next step is to create a Dagger pipeline to do the heavy lifting: build a container image of the application, release it to Google Container Registry and deploy it on Google Cloud Run.

<Tabs groupId="language">
<TabItem value="Go">

1. In the application directory, install the Dagger SDK and the Google Cloud Run client library as development dependencies:

  ```shell
  go get dagger.io/dagger@latest
  go get cloud.google.com/go/run/apiv2
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.go` and add the following code to it. Replace the PROJECT placeholder with your Google Cloud project identifier and adjust the region (`us-central1`) and service name (`myapp`) if you specified different values when creating the Google Cloud Run service in Step 2.

  ```go file=./snippets/github-google-cloud/main.go
  ```

  This file performs the following operations:
    - It imports the Dagger and Google Cloud Run client libraries.
    - It creates a Dagger client with `Connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `Host().Directory()` method to obtain a reference to the current directory on the host, excluding the `node_modules` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `Container().From()` method to initialize a new container from a base image. The additional `Platform` argument to the `Container()` method instructs Dagger to build for a specific architecture. In this example, the base image is the `node:16` image and the archiecture is `linux/amd64`, which is one of the architectures supported by Google Cloud. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `WithDirectory()` method to mount the host directory into the container at the `/src` mount point, and the `WithWorkdir()` method to set the working directory in the container.
    - It chains the `WithExec()` method to copy the contents of the working directory to the `/home/node` directory in the container and then uses the `WithWorkdir()` method to change the working directory in the container to `/home/node`.
    - It chains the `WithExec()` method again to install dependencies with `npm install` and sets the container entrypoint using the `WithEntrypoint()` method.
    - It uses the container object's `Publish()` method to publish the container to Google Container Registry, and prints the SHA identifier of the published image.
    - It creates a Google Cloud Run client, updates the Google Cloud Run service defined in Step 2 to use the published container image, and requests a service update.

1. Run the following command to update `go.sum`:

  ```shell
  go mod tidy
  ```

</TabItem>
<TabItem value="Node.js">

1. In the application directory, install the Dagger SDK and the Google Cloud Run client library as development dependencies:

  ```shell
  npm install @dagger.io/dagger@latest @google-cloud/run --save-dev
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `index.mjs` and add the following code to it. Replace the PROJECT placeholder with your Google Cloud project identifier and adjust the region (`us-central1`) and service name (`myapp`) if you specified different values when creating the Google Cloud Run service in Step 2.

  ```javascript file=./snippets/github-google-cloud/index.mjs
  ```

  This file performs the following operations:
    - It imports the Dagger and Google Cloud Run client libraries.
    - It creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `host().workdir()` method to obtain a reference to the current directory on the host, excluding the `node_modules` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `container().from()` method to initialize a new container from a base image. The additional `platform` argument to the `container()` method instructs Dagger to build for a specific architecture. In this example, the base image is the `node:16` image and the archiecture is `linux/amd64`, which is one of the architectures supported by Google Cloud. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `withDirectory()` method to mount the host directory into the container at the `/src` mount point, and the `withWorkdir()` method to set the working directory in the container.
    - It chains the `withExec()` method to copy the contents of the working directory to the `/home/node` directory in the container and then uses the `withWorkdir()` method to change the working directory in the container to `/home/node`.
    - It chains the `withExec()` method again to install dependencies with `npm install` and sets the container entrypoint using the `withEntrypoint()` method.
    - It uses the container object's `publish()` method to publish the container to Google Container Registry, and prints the SHA identifier of the published image.
    - It creates a Google Cloud Run client, updates the Google Cloud Run service defined in Step 2 to use the published container image, and requests a service update.

</TabItem>
<TabItem value="Python">

1. In the application directory, create a virtual environment and install the Dagger SDK and the Google Cloud Run client library:

  ```shell
  pip install dagger-io google-cloud-run
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.py` and add the following code to it. Replace the PROJECT placeholder with your Google Cloud project identifier and adjust the region (`us-central1`) and service name (`myapp`) if you specified different values when creating the Google Cloud Run service in Step 2.

  ```python file=./snippets/github-google-cloud/main.py
  ```

  This file performs the following operations:
    - It imports the Dagger and Google Cloud Run client libraries.
    - It creates a Dagger client with `dagger.Connection()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `host().directory()` method to obtain a reference to the current directory on the host, excluding the `node_modules` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `container().from_()` method to initialize a new container from a base image. The additional `platform` argument to the `container()` method instructs Dagger to build for a specific architecture. In this example, the base image is the `node:16` image and the archiecture is `linux/amd64`, which is one of the architectures supported by Google Cloud. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `with_directory()` method to mount the host directory into the container at the `/src` mount point, and the `with_workdir()` method to set the working directory in the container.
    - It chains the `withExec()` method to copy the contents of the working directory to the `/home/node` directory in the container and then uses the `withWorkdir()` method to change the working directory in the container to `/home/node`.
    - It chains the `with_exec()` method again to install dependencies with `npm install` and sets the container entrypoint using the `with_entrypoint()` method.
    - It uses the container object's `publish()` method to publish the container to Google Container Registry, and prints the SHA identifier of the published image.
    - It creates a Google Cloud Run client, updates the Google Cloud Run service defined in Step 2 to use the published container image, and requests a service update.

</TabItem>
</Tabs>

:::tip
Most `Container` object methods return a revised `Container` object representing the new state of the container. This makes it easy to chain methods together. Dagger evaluates pipelines "lazily", so the chained operations are only executed when required - in this case, when the container is published. Learn more about [lazy evaluation in Dagger](../api/975146-concepts.mdx#lazy-evaluation).
:::

## Step 4: Test the Dagger pipeline on the local host

Configure credentials for the Google Cloud SDK on the local host, as follows:

{@include: ../partials/_google-cloud-sdk-credentials-setup.md}

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

Dagger performs the operations defined in the pipeline script, logging each operation to the console. At the end of the process, the built container is deployed to Google Cloud Run and a message similar to the one below appears in the console output:

  ```shell
  Deployment for image gcr.io/PROJECT/myapp@sha256:b1cf... now available at https://...run.app
  ```

Browse to the URL shown in the deployment message to see the running application.

If you deployed the example application from [Appendix A](#appendix-a-create-a-github-repository-with-an-example-express-application), you should see a page similar to that shown below:

![Result of running pipeline from local host](/img/current/guides/github-google-cloud/local-deployment.png)

## Step 5: Create a GitHub Actions workflow

Dagger executes your pipelines entirely as standard OCI containers. This means that the same pipeline will run the same, whether on on your local machine or a remote server.

This also means that it's very easy to move your Dagger pipeline from your local host to GitHub Actions - all that's needed is to commit and push the pipeline script from your local clone to your GitHub repository, and then define a GitHub Actions workflow to run it on every commit.

1. Commit and push the pipeline script and related changes to the application's GitHub repository:

  ```shell
  git add .
  git commit -a -m "Added pipeline"
  git push
  ```

1. In the GitHub repository, create a new workflow file at `.github/workflows/main.yml` with the following content:

  <Tabs groupId="language">
  <TabItem value="Go">

  ```yaml file=./snippets/github-google-cloud/github-go.yml
  ```

  </TabItem>
  <TabItem value="Node.js">

  ```yaml file=./snippets/github-google-cloud/github-nodejs.yml
  ```

  </TabItem>
  <TabItem value="Python">

  ```yaml file=./snippets/github-google-cloud/github-python.yml
  ```

  </TabItem>
  </Tabs>

  This workflow runs on every commit to the repository `master` branch. It consists of a single job with six steps, as below:
    - The first step uses the [Checkout action](https://github.com/marketplace/actions/checkout) to check out the latest source code from the `main` branch to the GitHub runner.
    - The second step uses the [Authenticate to Google Cloud action](https://github.com/marketplace/actions/authenticate-to-google-cloud) to authenticate to Google Cloud. It requires a service account key in JSON format, which it expects to find in the `GOOGLE_CREDENTIALS` GitHub secret. This step sets various environment variables (including the GOOGLE_APPLICATION_CREDENTIALS variable required by the Google Cloud Run SDK) and returns an access token as output, which is used to authenticate the next step.
    - The third step uses the [Docker Login action](https://github.com/marketplace/actions/docker-login) and the access token from the previous step to authenticate to Google Container Registry from the GitHub runner. This is necessary because Dagger relies on the host's Docker credentials and authorizations when publishing to remote registries.
    - The fourth and fifth steps download and install the programming language and required dependencies (such as the Dagger SDK and the Google Cloud Run SDK) on the GitHub runner.
    - The sixth and final step executes the Dagger pipeline.

The [Authenticate to Google Cloud action](https://github.com/marketplace/actions/authenticate-to-google-cloud) looks for a JSON service account key in the `GOOGLE_CREDENTIALS` GitHub secret. Create this secret as follows:

1. Navigate to the `Settings` -> `Secrets` -> `Actions` page in the GitHub Web interface.
1. Click `New repository secret` to create a new secret.
1. Configure the secret with the following inputs:
    - Name: `GOOGLE_CREDENTIALS`
    - Secret: The contents of the service account JSON key file downloaded in Step 1.
1. Click `Add secret` to save the secret.

![Create GitHub secret](/img/current/guides/github-google-cloud/create-github-secret.png)

## Step 6: Test the Dagger pipeline on GitHub

Test the Dagger pipeline by committing a change to the GitHub repository.

If you are using the example application described in [Appendix A](#appendix-a-create-a-github-repository-with-an-example-express-application), the following commands modify and commit a simple change to the application's index page:

```shell
git pull
sed -i 's/Dagger/Dagger on GitHub/g' routes/index.js
git add routes/index.js
git commit -a -m "Update welcome message"
git push
```

The commit triggers the GitHub Actions workflow defined in Step 6. The workflow runs the various steps of the `dagger` job, including the pipeline script.

At the end of the process, a new version of the built container image is released to Google Container Registry and deployed on Google Cloud Run. A message similar to the one below appears in the GitHub Actions log:

```shell
Deployment for image gcr.io/PROJECT/myapp@sha256:h4si... now available at https://...run.app
```

Browse to the URL shown in the deployment message to see the running application. If you deployed the example application with the additional modification above, you see a page similar to that shown below:

![Result of running pipeline from GitHub](/img/current/guides/github-google-cloud/github-actions-deployment.png)

## Conclusion

This tutorial walked you through the process of creating a Dagger pipeline to continuously build and deploy a Node.js application on Google Cloud Run. It used the Dagger SDKs and explained key concepts, objects and methods available in the SDKs to construct a Dagger pipeline.

Dagger executes your pipelines entirely as standard OCI containers. This means that pipelines can be tested and debugged locally, and that the same pipeline will run consistently on your local machine, a CI runner, a dedicated server, or any container hosting service. This portability is one of Dagger's key advantages, and this tutorial demonstrated it in action by using the same pipeline on the local host and on GitHub.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.

## Appendix A: Create a GitHub repository with an example Express application

This tutorial assumes that you have a GitHub repository with a Node.js Web application. If not, follow the steps below to create a GitHub repository and commit an example Express application to it.

1. Log in to GitHub using the GitHub CLI:

  ```shell
  gh auth login
  ```

1. Create a directory for the Express application:

  ```shell
  mkdir myapp
  cd myapp
  ```

1. Create a skeleton Express application:

  ```shell
  npx express-generator
  ```

1. Make a minor modification to the application's index page:

  ```shell
  sed -i -e 's/Express/Dagger/g' routes/index.js
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

1. Create a private repository in your GitHub account and push the changes to it:

  ```shell
  gh repo create myapp --push --source . --private
  ```
