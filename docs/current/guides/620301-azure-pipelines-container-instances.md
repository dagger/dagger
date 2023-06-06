---
slug: /620301/azure-pipelines-container-instances
displayed_sidebar: "current"
category: "guides"
tags: ["nodejs", "go", "python", "azure", "azure-pipelines", "azure-container-instances"]
authors: ["Vikram Vaswani"]
date: "2023-05-30"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Use Dagger with Azure Pipelines and Azure Container Instances

## Introduction

This tutorial teaches you how to use Dagger to continuously build and deploy a Node.js application to Azure Container Instances with Azure Pipelines. You will learn how to:

- Configure an Azure resource group and service principal
- Create a Dagger pipeline using a Dagger SDK
- Run the Dagger pipeline on your local host to manually build and deploy the application to Azure Container Instances
- Use the same Dagger pipeline with Azure Pipelines to automatically build and deploy the application to Azure Container Instances on every repository commit

## Requirements

This tutorial assumes that:

- You have a basic understanding of the JavaScript programming language.
- You have a basic understanding of Azure DevOps and Azure Container Instances. If not, learn about [Azure DevOps](https://learn.microsoft.com/en-us/azure/devops) and [Azure Container Instances](https://azure.microsoft.com/en-us/products/container-instances).
- You have a Go, Python or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have the Azure CLI installed. If not, [install the Azure CLI](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli).
- You have a Docker Hub account. If not, [register for a free Docker Hub account](https://hub.docker.com/signup).
- You have an Azure subscription with "Owner" (or higher) privileges. If not, [register for an Azure account](https://azure.microsoft.com/en-us/free/).
- You have an Azure DevOps project containing a Node.js Web application. This repository should also be cloned locally in your development environment. If not, follow the steps in Appendix A to [create and populate a local and Azure DevOps repository with an example Express application](#appendix-a-create-an-azure-devops-repository-with-an-example-express-application).

## Step 1: Create an Azure resource group and service principal

The first step is to create an Azure resource group for the container instance, as well as an Azure service principal for the Dagger pipeline.

1. Log in to Azure using the Azure CLI:

  ```shell
  az login
  ```

1. Create a new Azure resource group (in this example, a group named `mygroup` in the `useast` location):

  ```shell
  az group create --location eastus --name my-group
  ```

  Note the resource group ID (`id` field) in the output, as you will need it when creating the service principal.

1. Create a service principal for the application (here, a principal named `mydaggerprincipal`) and assign it the "Contributor" role. Replace the `RESOURCE-GROUP-ID` placeholder in the command with the resource group ID obtained from the previous command.

  ```shell
  az ad sp create-for-rbac --name my-dagger-principal  --role Contributor --scopes RESOURCE-GROUP-ID
  ```

  :::info
  The "Contributor" role gives the service principal access to manage all resources in the group, including container instances.
  :::

  The output of the previous command contains the credentials for the service principal, including the client ID (`appId` field), tenant ID (`tenant` field) and client secret (`password` field). Note these values carefully, as they will not be shown again and you will need them in subsequent steps.

## Step 2: Create the Dagger pipeline

The next step is to create a Dagger pipeline to do the heavy lifting: build a container image of the application, release it to Docker Hub and deploy it on Azure Container Instances using the service principal from the previous step.

<Tabs groupId="language">
<TabItem value="Go">

1. In the application directory, install the Dagger SDK and the Azure SDK client libraries:

  ```shell
  go mod init main
  go get dagger.io/dagger@latest
  go get github.com/Azure/azure-sdk-for-go/sdk/azcore
  go get github.com/Azure/azure-sdk-for-go/sdk/azidentity
  go get github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerinstance/armcontainerinstance/v2
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.go` and add the following code to it. Modify the region (`useast`) and resource group name (`my-group`) if you specified different values when creating the Azure resource group in Step 1.

  ```go file=./snippets/azure-pipelines-container-instances/main.go
  ```

  This file performs the following operations:
    - It imports the Dagger and Azure SDK libraries.
    - It checks for various required credentials in the host environment.
    - It creates a Dagger client with `Connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `SetSecret()` method to set the Docker Hub registry password as a secret for the Dagger pipeline.
    - It uses the client's `Host().Directory()` method to obtain a reference to the current directory on the host, excluding the `node_modules` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `Container().From()` method to initialize a new container from a base image. The additional `Platform` argument to the `Container()` method instructs Dagger to build for a specific architecture. In this example, the base image is the `node:18` image and the architecture is `linux/amd64`. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `WithDirectory()` method to mount the host directory into the container image at the `/src` mount point, and the `WithWorkdir()` method to set the working directory in the container image.
    - It chains the `WithExec()` method to copy the contents of the working directory to the `/home/node` directory in the container image and then uses the `WithWorkdir()` method to change the working directory in the container image to `/home/node`.
    - It chains the `WithExec()` method again to install dependencies with `npm install` and sets the container entrypoint using the `WithEntrypoint()` method.
    - It uses the container object's `WithRegistryAuth()` method to set the registry credentials (including the password set as a secret previously) and then invokes the `Publish()` method to publish the container image to Docker Hub. It also prints the SHA identifier of the published image.
    - It creates an Azure client (using the Azure credentials set in the host environment)
    - It defines a deployment request to create or update a container in the Azure Container Instances service. This deployment request includes the container name, image, port configuration, location and other details.
    - It submits the deployment request to the Azure Container Instances service and waits for a response. If successful, it prints the public IP address of the running container image.

1. Run the following command to update `go.sum`:

  ```shell
  go mod tidy
  ```

</TabItem>
<TabItem value="Node.js">

1. In the application directory, install the Dagger SDK and the Azure SDK client libraries as development dependencies:

  ```shell
  npm install @dagger.io/dagger@latest @azure/arm-containerinstance @azure/identity --save-dev
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `index.mjs` and add the following code to it. Modify the region (`useast`) and resource group name (`my-group`) if you specified different values when creating the Azure resource group in Step 1.

  ```javascript file=./snippets/azure-pipelines-container-instances/index.mjs
  ```

  This file performs the following operations:
    - It imports the Dagger and Azure SDK libraries.
    - It checks for various required credentials in the host environment.
    - It creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `setSecret()` method to set the Docker Hub registry password as a secret for the Dagger pipeline.
    - It uses the client's `host().directory()` method to obtain a reference to the current directory on the host, excluding the `node_modules` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `container().from()` method to initialize a new container from a base image. The additional `platform` argument to the `container()` method instructs Dagger to build for a specific architecture. In this example, the base image is the `node:18` image and the architecture is `linux/amd64`. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `withDirectory()` method to mount the host directory into the container image at the `/src` mount point, and the `withWorkdir()` method to set the working directory in the container image.
    - It chains the `withExec()` method to copy the contents of the working directory to the `/home/node` directory in the container image and then uses the `withWorkdir()` method to change the working directory in the container image to `/home/node`.
    - It chains the `withExec()` method again to install dependencies with `npm install` and sets the container entrypoint using the `withEntrypoint()` method.
    - It uses the container object's `withRegistryAuth()` method to set the registry credentials (including the password set as a secret previously) and then invokes the `publish()` method to publish the container image to Docker Hub. It also prints the SHA identifier of the published image.
    - It creates an Azure client (using the Azure credentials set in the host environment)
    - It defines a deployment request to create or update a container in the Azure Container Instances service. This deployment request includes the container name, image, port configuration, location and other details.
    - It submits the deployment request to the Azure Container Instances service and waits for a response. If successful, it prints the public IP address of the running container image.

</TabItem>
<TabItem value="Python">

1. In the application directory, create a virtual environment and install the Dagger SDK and the Azure SDK client libraries:

  ```shell
  pip install dagger-io aiohttp azure-identity azure-mgmt-containerinstance
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.py` and add the following code to it. Modify the region (`useast`) and resource group name (`my-group`) if you specified different values when creating the Azure resource group in Step 1.

  ```python file=./snippets/azure-pipelines-container-instances/main.py
  ```

  This file performs the following operations:
    - It imports the Dagger and Azure SDK libraries.
    - It checks for various required credentials in the host environment.
    - It creates a Dagger client with `dagger.Connection()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `set_secret()` method to set the Docker Hub registry password as a secret for the Dagger pipeline.
    - It uses the client's `host().directory()` method to obtain a reference to the current directory on the host, excluding the `node_modules` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `container().from_()` method to initialize a new container from a base image. The additional `platform` argument to the `container()` method instructs Dagger to build for a specific architecture. In this example, the base image is the `node:18` image and the architecture is `linux/amd64`. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `with_directory()` method to mount the host directory into the container image at the `/src` mount point, and the `with_workdir()` method to set the working directory in the container image.
    - It chains the `with_exec()` method to copy the contents of the working directory to the `/home/node` directory in the container image and then uses the `with_eorkdir()` method to change the working directory in the container image to `/home/node`.
    - It chains the `with_exec()` method again to install dependencies with `npm install` and sets the container entrypoint using the `with_entrypoint()` method.
    - It uses the container object's `with_registry_auth()` method to set the registry credentials (including the password set as a secret previously) and then invokes the `publish()` method to publish the container image to Docker Hub. It also prints the SHA identifier of the published image.
    - It creates an Azure client (using the Azure credentials set in the host environment)
    - It defines a deployment request to create or update a container in the Azure Container Instances service. This deployment request includes the container name, image, port configuration, location and other details.
    - It submits the deployment request to the Azure Container Instances service and waits for a response. If successful, it prints the public IP address of the running container image.

</TabItem>
</Tabs>

:::tip
Most `Container` object methods return a revised `Container` object representing the new state of the container. This makes it easy to chain methods together. Dagger evaluates pipelines "lazily", so the chained operations are only executed when required - in this case, when the container is published. Learn more about [lazy evaluation in Dagger](../api/975146-concepts.mdx#lazy-evaluation).
:::

## Step 3: Test the Dagger pipeline on the local host

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

## Step 4: Create an Azure Pipeline for Dagger

Dagger executes your pipelines entirely as standard OCI containers. This means that the same pipeline will run the same, whether on on your local machine or a remote server.

This also means that it's very easy to move your Dagger pipeline from your local host to Azure Pipelines - all that's needed is to commit and push the Dagger pipeline script from your local clone to your Azure DevOps repository, and then define an Azure Pipeline to run it on every commit.

1. Commit and push the Dagger pipeline script to the application's repository:

  ```shell
  git add .
  git commit -a -m "Added pipeline"
  git push
  ```

1. Create a new Azure Pipeline:

  ```shell
  az pipelines create --name dagger --repository my-app --branch master --repository-type tfsgit --yml-path azure-pipelines.yml --skip-first-run true
  ```

1. Configure credentials for the Docker Hub registry and the Azure SDK in the Azure Pipeline by executing the commands below, replacing the placeholders as follows:

    - Replace the `TENANT-ID`, `CLIENT-ID` and `CLIENT-SECRET` placeholders with the service principal credentials obtained at the end of Step 1.
    - Replace the `SUBSCRIPTION-ID` placeholder with your Azure subscription ID.
    - Replace the `USERNAME` and `PASSWORD` placeholders with your Docker Hub username and password respectively.

  ```shell
  az pipelines variable create --name AZURE_TENANT_ID --value TENANT-ID --pipeline-name dagger
  az pipelines variable create --name AZURE_CLIENT_ID --value CLIENT-ID --pipeline-name dagger
  az pipelines variable create --name AZURE_CLIENT_SECRET --value CLIENT-SECRET --pipeline-name dagger --secret true
  az pipelines variable create --name AZURE_SUBSCRIPTION_ID --value SUBSCRIPTION-ID --pipeline-name dagger
  az pipelines variable create --name DOCKERHUB_USERNAME --value USERNAME --pipeline-name dagger
  az pipelines variable create --name DOCKERHUB_PASSWORD --value PASSWORD --pipeline-name dagger --secret true
  ```

1. In the repository, create a new file at `azure-pipelines.yml` with the following content:

  <Tabs groupId="language">
  <TabItem value="Go">

  ```yaml file=./snippets/azure-pipelines-container-instances/azure-pipeline-go.yml
  ```

  </TabItem>
  <TabItem value="Node.js">

  ```yaml file=./snippets/azure-pipelines-container-instances/azure-pipeline-nodejs.yml
  ```

  </TabItem>
  <TabItem value="Python">

  ```yaml file=./snippets/azure-pipelines-container-instances/azure-pipeline-python.yml
  ```

  </TabItem>
  </Tabs>

  This Azure Pipeline runs on every commit to the repository `master` branch. It consists of a single job with three steps, as below:
    - The first step uses a language-specific task to download and install the programing language on the CI runner.
    - The second step downloads and installs the required dependencies (such as the Dagger SDK and the Azure SDK) on the CI runner.
    - The third step adds executes the Dagger pipeline. It also explicity adds those variables defined as secret to the CI runner environment (other variables are automatically injected by Azure Pipelines).

  :::tip
  Azure Pipelines automatically transfers pipeline variables to the CI runner environment, except for those marked as secret. Secret variables need to be explicitly defined in the Azure Pipelines configuration file.
  :::

## Step 5: Test the Dagger pipeline in Azure Pipelines

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

## Appendix A: Create an Azure DevOps repository with an example Express application

This tutorial assumes that you have an Azure DevOps repository with a Node.js Web application. If not, follow the steps below to create an Azure DevOps repository and commit an example Express application to it.

1. Create a directory for the Express application:

  ```shell
  mkdir my-app
  cd my-app
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

1. Log in to Azure using the Azure CLI:

  ```shell
  az login
  ```

1. Create a new Azure DevOps project and repository in your Azure DevOps organization. Replace the `ORGANIZATION-URL` placeholder with your Azure DevOps organization URL (usually of the form `https://dev.azure.com/...`).

  ```shell
  az devops configure --defaults organization=ORGANIZATION-URL
  az devops project create --name my-app
  ```

1. List the available repositories and note the value of the `sshUrl` and `webUrl` fields:

  ```shell
  az repos list --project my-app | grep "sshUrl\|webUrl"
  ```

1. Browse to the URL shown in the `webUrl` field and [configure SSH authentication for the repository](https://learn.microsoft.com/en-us/azure/devops/repos/git/use-ssh-keys-to-authenticate?view=azure-devops).

1. Add the Azure DevOps repository as a remote and push the application code to it. Replace the `SSH-URL` placeholder with the value of the `sshUrl` field from the previous command.

  ```shell
  git remote add origin SSH-URL
  git push -u origin --all
  ```
