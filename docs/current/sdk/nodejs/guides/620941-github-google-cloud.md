---
slug: /sdk/nodejs/620941/github-google-cloud
displayed_sidebar: "current"
---

# Use Dagger with GitHub Actions and Google Cloud

:::note
[Watch a live demo](https://youtu.be/-pKmv0VDJBg) of this tutorial in the Dagger Community Call (12 Jan 2023). For more demos, [join the next Dagger Community Call](https://dagger.io/events).
:::

## Introduction

This tutorial teaches you how to use a Dagger pipeline to continuously build and deploy a Node.js application with GitHub Actions on Google Cloud Run. You will learn how to:

- Configure a Google Cloud service account and assign it the correct roles
- Create a Google Cloud Run service accessible at a public URL
- Create a Dagger pipeline using the Node.js SDK
- Run the Dagger pipeline on your local host to manually build and deploy the application on Google Cloud Run
- Use the same Dagger pipeline with GitHub Actions to automatically build and deploy the application on Google Cloud Run on every repository commit

## Requirements

This tutorial assumes that:

- You have a basic understanding of the JavaScript programming language.
- You have a basic understanding of GitHub Actions. If not, [learn about GitHub Actions](https://docs.github.com/en/actions).
- You have a Node.js development environment with Node.js 16.x or later. If not, install [NodeJS](https://nodejs.org/en/download/).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have the Google Cloud CLI installed. If not, [install the Google Cloud CLI](https://cloud.google.com/sdk/docs/install).
- You have a Google Cloud account and a Google Cloud project with billing enabled. If not, [register for a Google Cloud account](https://cloud.google.com/), [create a Google Cloud project](https://console.cloud.google.com/project) and [enable billing](https://support.google.com/cloud/answer/6293499#enable-billing).
- You have a GitHub account and a GitHub repository containing a Node.js Web application. This repository should also be cloned locally in your development environment. If not, [register for a GitHub account](https://github.com/signup), [install the GitHub CLI](https://github.com/cli/cli#installation) and follow the steps in Appendix A to [create and populate a local and GitHub repository with an example Express application](#appendix-a-create-a-github-repository-with-an-example-express-application).

## Step 1: Create a Google Cloud service account

The Dagger pipeline demonstrated in this tutorial (re)builds a container image of an application every time a new commit is added to the application's GitHub repository. It then publishes the container image to Google Container registry and deploys it at a public URL using Google Cloud infrastructure.

This requires the following:

- A Google Cloud service account with all necessary privileges
- A Google Cloud Run service with a public URL and defined resource/capacity/access rules
- Access to various Google Cloud APIs

:::info
This step discusses how to create a Google Cloud service account. If you already have a Google Cloud service account and key for your project, skip to [Step 2](#step-2-configure-google-cloud-apis-and-a-google-cloud-run-service).
:::

The first step is to create a Google Cloud service account, as follows:

1. Log in to the Google Cloud Console and select your project.
1. From the navigation menu, click `IAM & Admin` -> `Service Accounts`.
1. Click `Create Service Account`.
1. In the `Service account details` section, enter a string in the `Service account ID` field. This string forms the prefix of the unique service account email address.

  ![Create Google Cloud service account](/img/current/sdk/nodejs/guides/github-google-cloud/create-gcloud-service-account-id.png)

1. Click `Create and Continue`.
1. In the `Grant this service account access to project` section, select the `Service Account Token Creator` and `Editor` roles.

  ![Create Google Cloud service account roles](/img/current/sdk/nodejs/guides/github-google-cloud/create-gcloud-service-account-role.png)

1. Click `Continue`.
1. Click `Done`.

Once the service account is created, the Google Cloud Console displays it in the service account list, as shown below. Note the service account email address, as you will need it in the next step.

  ![List Google Cloud service accounts](/img/current/sdk/nodejs/guides/github-google-cloud/list-gcloud-service-accounts.png)

Next, create a JSON key for the service account as follows:

1. From the navigation menu, click `IAM & Admin` -> `Service Accounts`.
1. Click the newly-created service account in the list of service accounts.
1. Click the `Keys` tab on the service account detail page.
1. Click `Add Key` -> `Create new key`.
1. Select the `JSON` key type.
1. Click `Create`.

The key is created and automatically downloaded to your local host through your browser as a JSON file.

  ![Create Google Cloud service account key](/img/current/sdk/nodejs/guides/github-google-cloud/create-gcloud-service-account-key.png)

:::warning
Store the JSON service account key file safely as it cannot be retrieved again.
:::

## Step 2: Configure Google Cloud APIs and a Google Cloud Run service

The next step is to enable access to the required Google Cloud APIs:

1. From the navigation menu, select the `APIs & Services` -> `Enabled APIs & services` option.
1. Select the `Enable APIs and Services` option.
1. On the `API Library` page, search for and select the `Cloud Run API` entry.
1. On the API detail page, click `Enable`.

  ![Enable Google Cloud API](/img/current/sdk/nodejs/guides/github-google-cloud/enable-gcloud-api.png)

1. Repeat the previous two steps for the `IAM Service Account Credentials API`.

Once the APIs are enabled, the Google Cloud Console displays the updated status of the APIs.

The final step is to create a Google Cloud Run service and corresponding public URL endpoint. This service will eventually host the container deployed by the Dagger pipeline.

1. From the navigation menu, select the `Serverless` -> `Cloud Run` product.
1. Select the `Create Service` option.
1. Select the `Deploy one revision from an existing container image` option. Click `Test with a sample container` to have a container image URL pre-filled.
1. Continue configuring the service with the following inputs:

    - Service name: `myapp` (modify as needed)
    - Region: `us-central1` (modify as needed)
    - CPU allocation and pricing: `CPU is only allocated during request processing`
    - Minimum number of instances: `0` (modify as needed)
    - Maximum number of instances: `1` (modify as needed)
    - Ingress: `Allow all traffic`
    - Authentication: `Allow unauthenticated invocations`

    ![Create Google Cloud Run service](/img/current/sdk/nodejs/guides/github-google-cloud/create-gcloud-run-service.png)

1. Click `Create` to create the service.

The new service is created. The Google Cloud Console displays the service details, including its public URL, on the service detail page, as shown below.

![View Google Cloud Run service details](/img/current/sdk/nodejs/guides/github-google-cloud/view-gcloud-run-service.png)

## Step 3: Create the Dagger pipeline

The next step is to create a Dagger pipeline to do the heavy lifting: build a container image of the application, release it to Google Container Registry and deploy it on Google Cloud Run.

1. In the application directory, install the Dagger SDK and the Google Cloud Run client library as development dependencies:

  ```shell
  npm install @dagger.io/dagger@latest @google-cloud/run --save-dev
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.mjs` and add the following code to it. Replace the PROJECT placeholder with your Google Cloud project identifier and adjust the region (`us-central1`) and service name (`myapp`) if you specified different values when creating the Google Cloud Run service in Step 2.

  ```javascript file=../snippets/github-google-cloud/main.mjs
  ```

  This file performs the following operations:
    - It imports the Dagger and Google Cloud Run client libraries.
    - It creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `host().workdir()` method to obtain a reference to the current directory on the host, excluding the `node_modules` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `container().from()` method to initialize a new container from a base image. In this example, the base image is the `node:16` image. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `withMountedDirectory()` method to mount the host directory into the container at the `/src` mount point, and the `withWorkdir()` method to set the working directory in the container.
    - It chains the `withExec()` method to copy the contents of the working directory to the `/home/node` directory in the container and then uses the `withWorkdir()` method to change the working directory in the container to `/home/node`.
    - It chains the `withExec()` method again to install dependencies with `npm install` and sets the container entrypoint using the `withEntrypoint()` method.
    - It uses the container object's `publish()` method to publish the container to Google Container Registry, and prints the SHA identifier of the published image.
    - It creates a Google Cloud Run client, updates the Google Cloud Run service defined in Step 2 to use the published container image, and requests a service update.

:::tip
Most `Container` object methods return a revised `Container` object representing the new state of the container. This makes it easy to chain methods together. Dagger evaluates pipelines "lazily", so the chained operations are only executed when required - in this case, when the `publish()` method is called.
:::

## Step 4: Test the Dagger pipeline on the local host

Test the Dagger pipeline as follows:

1. Configure Docker credentials for Google Container Registry on the local host using the following commands. Replace the SERVICE-ACCOUNT-ID placeholder with the service account email address created in Step 1, and the SERVICE-ACCOUNT-KEY-FILE placeholder with the location of the service account JSON key file downloaded in Step 1.

  ```shell
  gcloud auth activate-service-account SERVICE-ACCOUNT-ID --key-file=SERVICE-ACCOUNT-KEY-FILE
  gcloud auth configure-docker
  ```

  :::info
  This step is necessary because Dagger relies on the host's Docker credentials and authorizations when publishing to remote registries.
  :::

1. Set the `GOOGLE_APPLICATION_CREDENTIALS` environment variable to the location of the service account JSON key file, replacing the SERVICE-ACCOUNT-KEY-FILE placeholder in the following command. This variable is used by the Google Cloud Run client library during the client authentication process.

  ```shell
  export GOOGLE_APPLICATION_CREDENTIALS=SERVICE-ACCOUNT-KEY-FILE
  ```

1. Run the pipeline:

  ```shell
  node ci/main.mjs
  ```

Dagger performs the operations defined in the pipeline script, logging each operation to the console. At the end of the process, the built container is deployed to Google Cloud Run and a message similar to the one below appears in the console output:

  ```shell
  Deployment for image gcr.io/PROJECT/myapp@sha256:b1cf... now available at https://...run.app
  ```

Browse to the URL shown in the deployment message to see the running application.

If you deployed the example application from [Appendix A](#appendix-a-create-a-github-repository-with-an-example-express-application), you should see a page similar to that shown below:

![Result of running pipeline from local host](/img/current/sdk/nodejs/guides/github-google-cloud/local-deployment.png)

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

  ```yaml file=../snippets/github-google-cloud/main.yml
  ```

  This workflow runs on every commit to the repository `master` branch. It consists of a single job with six steps, as below:
    - The first step uses the [Checkout action](https://github.com/marketplace/actions/checkout) to check out the latest source code from the `main` branch to the GitHub runner.
    - The second step uses the [Authenticate to Google Cloud action](https://github.com/marketplace/actions/authenticate-to-google-cloud) to authenticate to Google Cloud. It requires a service account key in JSON format, which it expects to find in the `GOOGLE_CREDENTIALS` GitHub secret. This step sets various environment variables (including the GOOGLE_APPLICATION_CREDENTIALS variable required by the Google Cloud Run SDK) and returns an access token as output, which is used to authenticate the next step.
    - The third step uses the [Docker Login action](https://github.com/marketplace/actions/docker-login) and the access token from the previous step to authenticate to Google Container Registry from the GitHub runner. This is necessary because Dagger relies on the host's Docker credentials and authorizations when publishing to remote registries.
    - The fourth step uses the [Setup Node.js environment action](https://github.com/marketplace/actions/setup-node-js-environment) to download and install Node.js 16.x on the GitHub runner.
    - The fifth step downloads and installs the application's dependencies on the GitHub runner with `npm install`.
    - The sixth and final step executes the Dagger pipeline.

The [Authenticate to Google Cloud action](https://github.com/marketplace/actions/authenticate-to-google-cloud) looks for a JSON service account key in the `GOOGLE_CREDENTIALS` GitHub secret. Create this secret as follows:

1. Navigate to the `Settings` -> `Secrets` -> `Actions` page in the GitHub Web interface.
1. Click `New repository secret` to create a new secret.
1. Configure the secret with the following inputs:
    - Name: `GOOGLE_CREDENTIALS`
    - Secret: The contents of the service account JSON key file downloaded in Step 1.
1. Click `Add secret` to save the secret.

![Create GitHub secret](/img/current/sdk/nodejs/guides/github-google-cloud/create-github-secret.png)

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

![Result of running pipeline from GitHub](/img/current/sdk/nodejs/guides/github-google-cloud/github-actions-deployment.png)

## Conclusion

This tutorial walked you through the process of creating a Dagger pipeline to continuously build and deploy a Node.js application on Google Cloud Run. It used the Dagger Node.js SDK and explained key concepts, objects and methods available in the SDK to construct a Dagger pipeline.

Dagger executes your pipelines entirely as standard OCI containers. This means that pipelines can be tested and debugged locally, and that the same pipeline will run consistently on your local machine, a CI runner, a dedicated server, or any container hosting service. This portability is one of Dagger's key advantages, and this tutorial demonstrated it in action by using the same pipeline on the local host and on GitHub.

Use the [API Key Concepts](../../../api/975146-concepts.md) page and the [Node.js SDK Reference](../reference/modules.md) to learn more about Dagger.

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
