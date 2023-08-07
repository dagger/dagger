---
slug: /882813/build-test-publish-java-spring
displayed_sidebar: "current"
category: "guides"
tags: ["java", "spring"]
authors: ["Vikram Vaswani"]
date: "2023-07-05"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Build, Test and Publish a Spring Application with Dagger

## Introduction

Dagger SDKs are currently available for Go, Node.js and Python, but you can use them to create CI/CD pipelines for applications written in any programming language. This guide explains how to use Dagger to continuously build, test and publish a Java application using Spring. You will learn how to:

- Create a Dagger pipeline to:
  - Build your Spring application with all required dependencies
  - Run unit tests for your Spring application
  - Publish the final application image to Docker Hub
- Run the Dagger pipeline on the local host using the Dagger CLI
- Run the Dagger pipeline on every repository commit using GitHub Actions

## Requirements

This guide assumes that:

- You have a basic understanding of how Dagger works. If not, [read the Dagger Quickstart](../quickstart/index.mdx).
- You have a basic understanding of GitHub Actions. If not, [learn about GitHub Actions](https://docs.github.com/en/actions).
- You have Docker installed and running in your development environment. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have the Dagger CLI installed in your development environment. If not, [install the Dagger CLI](../cli/465058-install.md).
- You have a Docker Hub account. If not, [register for a free Docker Hub account](https://hub.docker.com/signup).
- You have a GitHub account. If not, [register for a free GitHub account](https://github.com/signup).
- You have a GitHub repository containing a Spring application. This repository should also be cloned locally in your development environment. If not, follow the steps in Appendix A to [create and populate a local and GitHub repository with a Spring sample application](#appendix-a-create-a-github-repository-with-an-example-spring-application).

## Step 1: Create the Dagger pipeline

The first step is to create a Dagger pipeline to build and test a container image of the application, and publish it to Docker Hub

<Tabs groupId="language">
<TabItem value="Go">

1. In the application directory, install the Dagger SDK:

  ```shell
  go mod init main
  go get dagger.io/dagger@latest
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.go` and add the following code to it.

  ```go file=./snippets/build-test-publish-java-spring/main.go
  ```

  This Dagger pipeline performs a number of different operations:
    - It imports the Dagger SDK and checks for Docker Hub registry credentials in the host environment. It also creates a Dagger client with `dagger.Connection()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `set_secret()` method to set the Docker Hub registry password as a secret for the Dagger pipeline and configures a Maven cache volume with the `cache_volume()` method. This cache volume is used to persist the state of the Maven cache between runs, thereby eliminating time spent on re-downloading Maven packages.
    - It uses the client's `host().directory()` method to obtain a reference to the source code directory on the host.
    - It uses the client's `container().from_()` method to initialize three new containers, each of which is returned as a `Container` object:
        - A MariaDB database service container from the `mariadb:10.11.2` image, for application unit tests;
        - A Maven container with all required tools and dependencies from the `maven:3.9-eclipse-temurin-17` image, to build and package the application JAR file;
        - An OpenJDK Eclipse Temurin container from the `eclipse-temurin:17-alpine` image, to create an optimized deployment package.
    - For the MariaDB database container:
        - It chains multiple `with_env_variable()` methods to configure the database service, and uses the `with_exposed_port()` method to ensure that the service is available to clients.
    -  For the Maven container:
        - It uses the `with_mounted_directory()` and `with_mounted_cache()` methods to mount the host directory and the cache volume into the Maven container at the `/src` and `/root/.m2` mount points, and the `with_workdir()` method to set the working directory in the container.
        - It adds a service binding for the database service to the Maven container using the `with_service_binding()` method and sets the JDBC URL for the application test suite as an environment using the `with_env_variable()` method.
        - Finally, it uses the `with_exec()` method to execute the `mvn -Dspring.profiles.active=mysql clean package` command, which builds, tests and creates a JAR package of the application.
    -  For the Eclipse Temurin container:
        - Once the JAR package is ready, it copies only the build artifact directory to the Eclipse Temurin container using the `with_directory()` method, and sets the container entrypoint to start the Spring application using the `with_entrypoint()` method.
    - It uses the `with_registry_auth()` method to set the registry credentials (including the password set as a secret previously) and then invokes the `publish()` method to publish the Eclipse Temurin container image to Docker Hub. It also prints the SHA identifier of the published image.

1. Run the following command to update `go.sum`:

  ```shell
  go mod tidy
  ```

</TabItem>
<TabItem value="Node.js">

1. Begin by installing the Dagger SDK as a development dependency:

    ```shell
    npm install @dagger.io/dagger@latest --save-dev
    ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `index.mjs` and add the following code to it.

    ```javascript file=./snippets/build-test-publish-java-spring/index.mjs
    ```

  This Dagger pipeline performs a number of different operations:
    - It imports the Dagger SDK and checks for Docker Hub registry credentials in the host environment. It also creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `setSecret()` method to set the Docker Hub registry password as a secret for the Dagger pipeline and configures a Maven cache volume with the `cacheVolume()` method. This cache volume is used to persist the state of the Maven cache between runs, thereby eliminating time spent on re-downloading Maven packages.
    - It uses the client's `host().directory()` method to obtain a reference to the source code directory on the host.
    - It uses the client's `container().from()` method to initialize three new containers, each of which is returned as a `Container` object:
        - A MariaDB database service container from the `mariadb:10.11.2` image, for application unit tests;
        - A Maven container with all required tools and dependencies from the `maven:3.9-eclipse-temurin-17` image, to build and package the application JAR file;
        - An OpenJDK Eclipse Temurin container from the `eclipse-temurin:17-alpine` image, to create an optimized deployment package.
    - For the MariaDB database container:
        - It chains multiple `withEnvVariable()` methods to configure the database service, and uses the `withExposedPort()` method to ensure that the service is available to clients.
    - For the Maven container:
        - It uses the `withMountedDirectory()` and `withMountedCache()` methods to mount the host directory and the cache volume into the Maven container at the `/src` and `/root/.m2` mount points, and the `withWorkdir()` method to set the working directory in the container.
        - It adds a service binding for the database service to the Maven container using the `withServiceBinding()` method and sets the JDBC URL for the application test suite as an environment using the `withEnvVariable()` method.
        - Finally, it uses the `withExec()` method to execute the `mvn -Dspring.profiles.active=mysql clean package` command, which builds, tests and creates a JAR package of the application.
    -  For the Eclipse Temurin container:
        - Once the JAR package is ready, it copies only the build artifact directory to the Eclipse Temurin container using the `withDirectory()` method, and sets the container entrypoint to start the Spring application using the `withEntrypoint()` method.
    - It uses the `withRegistryAuth()` method to set the registry credentials (including the password set as a secret previously) and then invokes the `publish()` method to publish the Eclipse Temurin container image to Docker Hub. It also prints the SHA identifier of the published image.

</TabItem>
<TabItem value="Python">

1. Begin by creating a virtual environment and installing the Dagger SDK:

  ```shell
  pip install dagger-io
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.py` and add the following code to it.

  ```python file=./snippets/build-test-publish-java-spring/main.py
  ```

  This Dagger pipeline performs a number of different operations:
    - It imports the Dagger SDK and checks for Docker Hub registry credentials in the host environment. It also creates a Dagger client with `dagger.Connection()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `set_secret()` method to set the Docker Hub registry password as a secret for the Dagger pipeline and configures a Maven cache volume with the `cache_volume()` method. This cache volume is used to persist the state of the Maven cache between runs, thereby eliminating time spent on re-downloading Maven packages.
    - It uses the client's `host().directory()` method to obtain a reference to the source code directory on the host.
    - It uses the client's `container().from_()` method to initialize three new containers, each of which is returned as a `Container` object:
        - A MariaDB database service container from the `mariadb:10.11.2` image, for application unit tests;
        - A Maven container with all required tools and dependencies from the `maven:3.9-eclipse-temurin-17` image, to build and package the application JAR file;
        - An OpenJDK Eclipse Temurin container from the `eclipse-temurin:17-alpine` image, to create an optimized deployment package.
    - For the MariaDB database container:
        - It chains multiple `with_env_variable()` methods to configure the database service, and uses the `with_exposed_port()` method to ensure that the service is available to clients.
    - For the Maven container:
        - It uses the `with_mounted_directory()` and `with_mounted_cache()` methods to mount the host directory and the cache volume into the Maven container at the `/src` and `/root/.m2` mount points, and the `with_workdir()` method to set the working directory in the container.
        - It adds a service binding for the database service to the Maven container using the `with_service_binding()` method and sets the JDBC URL for the application test suite as an environment using the `with_env_variable()` method.
        - Finally, it uses the `with_exec()` method to execute the `mvn -Dspring.profiles.active=mysql clean package` command, which builds, tests and creates a JAR package of the application.
    -  For the Eclipse Temurin container:
        - Once the JAR package is ready, it copies only the build artifact directory to the Eclipse Temurin container using the `with_directory()` method, and sets the container entrypoint to start the Spring application using the `with_entrypoint()` method.
    - It uses the `with_registry_auth()` method to set the registry credentials (including the password set as a secret previously) and then invokes the `publish()` method to publish the Eclipse Temurin container image to Docker Hub. It also prints the SHA identifier of the published image.

</TabItem>
</Tabs>

## Step 2: Test the Dagger pipeline on the local host

Configure the registry credentials using environment variables on the local host. Replace the `USERNAME` and `PASSWORD` placeholders with your Docker Hub credentials.

```shell
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

Dagger performs the operations defined in the pipeline script, logging each operation to the console. At the end of the process, the built container is published on Docker Hub and a message similar to the one below appears in the console output:

```shell
Image published at: docker.io/.../myapp@sha256:...
```

## Step 3: Create a GitHub Actions workflow

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

  ```yaml file=./snippets/build-test-publish-java-spring/github-go.yml
  ```

  </TabItem>
  <TabItem value="Node.js">

  ```yaml file=./snippets/build-test-publish-java-spring/github-nodejs.yml
  ```

  </TabItem>
  <TabItem value="Python">

  ```yaml file=./snippets/build-test-publish-java-spring/github-python.yml
  ```

  </TabItem>
  </Tabs>

  This workflow runs on every commit to the repository `main` branch. It consists of a single job with five steps, as below:
    1. The first step uses the [Checkout action](https://github.com/marketplace/actions/checkout) to check out the latest source code from the `main` branch to the GitHub runner.
    1. The second step uses the [Docker Login action](https://github.com/marketplace/actions/docker-login) to authenticate to Docker Hub from the GitHub runner. This is necessary because [Docker rate-limits unauthenticated registry pulls](https://docs.docker.com/docker-hub/download-rate-limit/).
    1. The third step downloads and installs the required programming language on the GitHub runner.
    1. The fourth step downloads and installs the Dagger SDK on the GitHub runner.
    1. The final step executes the Dagger pipeline.

The Docker Login action and the Dagger pipeline both expect to find Docker Hub credentials in the `DOCKERHUB_USERNAME` and `DOCKERHUB_PASSWORD` variables. Create these variables as GitHub secrets as follows:

1. Navigate to the `Settings` -> `Secrets and variables` -> `Actions` page of the GitHub repository.
1. Click `New repository secret` to create a new secret.
1. Configure the secret with the following inputs:
    - Name: `DOCKERHUB_USERNAME`
    - Secret: Your Docker Hub username
1. Click `Add secret` to save the secret.
1. Repeat the process for the `DOCKERHUB_PASSWORD` variable.

![Create GitHub secret](/img/current/guides/build-test-publish-java-spring/create-github-secret.png)

## Step 4: Test the Dagger pipeline on GitHub

Test the Dagger pipeline by committing a change to the GitHub repository.

If you are using the Spring Petclinic example application described in [Appendix A](#appendix-a-create-a-github-repository-with-an-example-spring-application), the following commands modify and commit a simple change to the application's welcome page:

```shell
git pull
sed -i -e "s/Welcome/Welcome from Dagger/g" src/main/resources/messages/messages.properties
git add src/main/resources/messages/messages.properties
git commit -a -m "Update welcome message"
git push
```

The commit triggers the GitHub Actions workflow defined in Step 3. The workflow runs the various steps of the job, including the pipeline script.

At the end of the process, a new version of the built container image is published to Docker Hub. A message similar to the one below appears in the GitHub Actions log:

```shell
Image published at: docker.io/.../myapp@sha256:...
```

Test the container, replacing `IMAGE-ADDRESS` with the image address returned by the pipeline.

```shell
docker run --rm --detach --net=host --name mariadb -e MYSQL_USER=user -e MYSQL_PASSWORD=password -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=db mariadb:10.11.2
docker run --rm --net=host -e MYSQL_URL=jdbc:mysql://user:password@localhost/db IMAGE-ADDRESS
```

Browse to host port 8080. If you are using the Spring Petclinic example application described in [Appendix A](#appendix-a-create-a-github-repository-with-an-example-spring-application), you see the page shown below:

![Application welcome page](/img/current/guides/build-test-publish-java-spring/test-container.png)

## Conclusion

Dagger SDKs are currently available for Go, Node.js and Python, but you can use Dagger to create CI/CD pipelines for applications written in any programming language. This tutorial demonstrated by creating a Dagger pipeline to build, test and publish a Spring application. A similar approach can be followed for any application, regardless of which programming language it's written in.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.

## Appendix A: Create a GitHub repository with an example Spring application

This tutorial assumes that you have a GitHub repository with a Spring application. If not, follow the steps below to create a GitHub repository and commit an example Express application to it.

:::info
This section assumes that you have the GitHub CLI. If not, [install the GitHub CLI](https://github.com/cli/cli#installation) before proceeding.
:::

1. Log in to GitHub using the GitHub CLI:

  ```shell
  gh auth login
  ```

1. Create a directory for the Spring application:

  ```shell
  mkdir myapp
  cd myapp
  ```

1. Clone the [Spring Petclinic sample application](https://github.com/spring-projects/spring-petclinic):

  ```shell
  git clone git@github.com:spring-projects/spring-petclinic.git .
  ```

1. Update the `.gitignore` file:

  ```shell
  echo node_modules >> .gitignore
  echo package*.json >> .gitignore
  echo .venv >> .gitignore
  git add .
  git commit -m "Updated .gitignore"
  ```

1. Remove existing GitHub Action workflows:

  ```shell
  rm -rf .github/workflows/*
  git add .
  git commit -m "Removed workflows"
  ```

1. Create a private repository in your GitHub account and push the code to it:

  ```shell
  gh repo create myapp --push --source . --private --remote github
  ```
