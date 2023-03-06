---
slug: /759201/gitlab-google-cloud
displayed_sidebar: "current"
category: "guides"
tags: ["go", "gitlab-ci", "google-cloud"]
authors: ["Vikram Vaswani"]
date: "2023-02-11"
---

# Use Dagger with GitLab CI/CD and Google Cloud

## Introduction

This tutorial teaches you how to use a Dagger pipeline to continuously build and deploy a Go application with GitLab on Google Cloud Run. You will learn how to:

- Configure a Google Cloud service account and assign it the correct roles
- Create a Google Cloud Run service accessible at a public URL
- Create a Dagger pipeline using the Go SDK
- Run the Dagger pipeline on your local host to manually build and deploy the application on Google Cloud Run
- Use the same Dagger pipeline with GitLab CI/CD to automatically build and deploy the application on Google Cloud Run on every repository commit

## Requirements

This tutorial assumes that:

- You have a basic understanding of the Go programming language.
- You have a basic understanding of GitLab and GitLab CI/CD. If not, [learn about GitLab CI/CD](https://docs.gitlab.com/ee/ci/).
- You have a Go development environment with Go 1.19.x or later. If not, install [Go](https://go.dev/doc/install).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have the Google Cloud CLI installed. If not, [install the Google Cloud CLI](https://cloud.google.com/sdk/docs/install).
- You have a Google Cloud account and a Google Cloud project with billing enabled. If not, [register for a Google Cloud account](https://cloud.google.com/), [create a Google Cloud project](https://console.cloud.google.com/project) and [enable billing](https://support.google.com/cloud/answer/6293499#enable-billing).
- You have a GitLab account and a GitLab repository containing a Go web application. This repository should also be cloned locally in your development environment. If not, [register for a GitLab account](https://gitlab.com/), [install the GitLab CLI](https://gitlab.com/gitlab-org/cli#installation) and follow the steps in Appendix A to [create and populate a local and GitLab repository with an example Go application](#appendix-a-create-a-gitlab-repository-with-an-example-go-application).
- You have a GitLab Runner application available to run your GitLab CI/CD pipeline. This could be either a self-hosted runner or a GitLab-managed runner. [Learn about GitLab Runner](https://docs.gitlab.com/runner/) and follow the steps in Appendix B to [configure a self-hosted runner](#appendix-b-configure-a-self-hosted-gitlab-runner-for-use-with-dagger).

## Step 1: Create a Google Cloud service account

{@include: ../partials/_google-cloud-service-account-key-setup.md}

## Step 2: Configure Google Cloud APIs and a Google Cloud Run service

{@include: ../partials/_google-cloud-api-run-setup.md}

## Step 3: Create the Dagger pipeline

The next step is to create a Dagger pipeline to do the heavy lifting: build a container image of the application, release it to Google Container Registry and deploy it on Google Cloud Run.

1. In the application directory, install the Dagger SDK and the Google Cloud Run client library:

  ```shell
  go get dagger.io/dagger@latest
  go get cloud.google.com/go/run/apiv2
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.go` and add the following code to it. Replace the PROJECT placeholder with your Google Cloud project identifier and adjust the region (`us-central1`) and service name (`myapp`) if you specified different values when creating the Google Cloud Run service in Step 2.

  ```go file=./snippets/gitlab-google-cloud/main.go
  ```

  This code listing performs the following operations:
    - It imports the Dagger and Google Cloud Run client libraries.
    - It creates a Dagger client with `Connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `Host().Workdir()` method to obtain a reference to the current directory on the host, excluding the `ci` directory. This reference is stored in the `source` variable.
    - In the first stage of the build, it uses the client's `Container().From()` method to initialize a new container from a base image. The additional `Platform` argument to the `Container()` method instructs Dagger to build for a specific architecture. In this example, the base image is the `golang:1.19` image and the architecture is `linux/amd64`, which is one of the architectures supported by Google Cloud. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `WithMountedDirectory()` method to mount the host directory into the container at the `/src` mount point, and the `WithWorkdir()` method to set the working directory in the container.
    - It chains the `WithEnvVariable()` method to set the `CGO_ENABLED` variable in the container environment and the `WithExec()` method to compile the Go application with `go build`.
    - Once the application is built, it moves to the second stage of the build. It again uses the client's `Container().From()` method to initialize a new container from an `alpine` base image.
    - It uses the previous `Container` object's `WithFile()` method to transfer the compiled binary file from the first stage to the new container filesystem.
    - It sets the container entrypoint to the binary file using the `withEntrypoint()` method.
    - It uses the container object's `Publish()` method to publish the container to Google Container Registry, and prints the SHA identifier of the published image.
    - It creates a Google Cloud Run client, creates a service request instructing the Google Cloud Run service to use the newly-published container image, and sends the requests to the Google Cloud Run API.

  :::tip
  Most `Container` object methods return a revised `Container` object representing the new state of the container. This makes it easy to chain methods together. Dagger evaluates pipelines "lazily", so the chained operations are only executed when required - in this case, when the `Publish()` method is called.
  :::

1. Run the following command to update `go.sum`:

  ```shell
  go mod tidy
  ```

## Step 4: Test the Dagger pipeline on the local host

Configure credentials for the Google Cloud SDK on the local host, as follows:

{@include: ../partials/_google-cloud-sdk-credentials-setup.md}

Once credentials are configured, test the Dagger pipeline by running the command below:

```shell
go run ci/main.go
```

Dagger performs the operations defined in the pipeline script, logging each operation to the console. At the end of the process, the built container is deployed to Google Cloud Run and a message similar to the one below appears in the console output:

  ```shell
  Deployment for image gcr.io/PROJECT/myapp@sha256:b1cf... now available at https://...run.app
  ```

Browse to the URL shown in the deployment message to see the running application.

If you deployed the example application from [Appendix A](#appendix-a-create-a-gitlab-repository-with-an-example-go-application), you see the output below:

```shell
Hello, Dagger!
```

## Step 5: Create a GitLab CI/CD pipeline

Dagger executes your pipelines entirely as standard OCI containers. This means that the same pipeline will run the same, whether on on your local machine or a remote server.

This also means that it's very easy to move your Dagger pipeline from your local host to GitLab - all that's needed is to transfer the pipeline script from your local clone to your GitLab repository, and then define a GitLab CI/CD pipeline to run it on every commit.

1. Create a new GitLab CI/CD pipeline configuration file in your application directory at `.gitlab-ci.yml` with the following content:

  ```yaml file=./snippets/gitlab-google-cloud/gitlab-ci.yml
  ```

  This GitLab CI/CD pipeline runs on every commit to the repository `master` branch. It consists of three jobs, as below:
    - The first job tells the GitLab runner to use the Docker executor with a `golang` base image and a Docker-in-Docker (`dind`) service. It also configures TLS and sets the location for Docker to generate its TLS certificates.
    - The second job adds the Docker CLI and authenticates to Google Container Registry from the GitLab runner. This is necessary because Dagger relies on the host's Docker credentials and authorizations when publishing to remote registries. For authentication, the job relies on the Google Cloud service account credentials, which are stored in the `GOOGLE_APPLICATION_CREDENTIALS` variable (more on this later).
    - The third and final job executes the Dagger pipeline code.

1. This GitLab CI/CD pipeline looks for a Google Cloud service account key in the `GOOGLE_APPLICATION_CREDENTIALS` GitLab variable. Create this variable as follows:

    1. Navigate to the `Settings` -> `CI/CD` -> `Variables` page in the GitLab Web interface.
    1. Click `Add variable` to create a new variable.
    1. Configure the variable with the following inputs:
        - Name: `GOOGLE_APPLICATION_CREDENTIALS`
        - Value: The contents of the service account JSON key file downloaded in Step 1
        - Type: `File`
        - Flags: `Protect variable`
    1. Click `Add variable` to save the variable.

    ![Create GitLab variable](/img/current/guides/gitlab-google-cloud/create-gitlab-variable.png)

1. Commit and push the changes to the GitLab repository:

  ```shell
  git add .
  git commit -a -m "Added pipeline and CI code"
  git push
  ```

## Step 6: Test the Dagger pipeline on GitLab

:::info
This step requires a properly-configured GitLab Runner. Refer to Appendix B for instructions on how to [configure a self-hosted GitLab Runner for use with Dagger](#appendix-b-configure-a-self-hosted-gitlab-runner-for-use-with-dagger).
:::

Test the Dagger pipeline by committing a change to the GitLab repository.

If you are using the example application described in [Appendix A](#appendix-a-create-a-gitlab-repository-with-an-example-go-application), the following commands modify and commit a simple change to the application's index page:

```shell
git pull
sed -i -e 's/Dagger/Dagger on GitLab/g' server.go
git commit -a -m "Update welcome message"
git push
```

The commit triggers the GitLab CI/CD pipeline defined in Step 6. The pipeline runs the various jobs, including the Dagger pipeline.

At the end of the process, a new version of the built container image is released to Google Container Registry and deployed on Google Cloud Run. A message similar to the one below appears in the GitHub Actions log:

```shell
Deployment for image gcr.io/PROJECT/myapp@sha256:h4si... now available at https://...run.app
```

Browse to the URL shown in the deployment message to see the running application. If you deployed the example application with the additional modification above, you see the following output:

```shell
Hello, Dagger on GitLab!
```

## Conclusion

This tutorial walked you through the process of creating a Dagger pipeline to continuously build and deploy a Go application on Google Cloud Run. It used the Dagger Go SDK and explained key concepts, objects and methods available in the SDK to construct a Dagger pipeline.

Dagger executes your pipelines entirely as standard OCI containers. This means that pipelines can be tested and debugged locally, and that the same pipeline will run consistently on your local machine, a CI runner, a dedicated server, or any container hosting service. This portability is one of Dagger's key advantages, and this tutorial demonstrated it in action by using the same pipeline on the local host and on GitLab.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go SDK Reference](https://pkg.go.dev/dagger.io/dagger) to learn more about Dagger.

## Appendix A: Create a GitLab repository with an example Go application

This tutorial assumes that you have a GitLab repository with a application. If not, follow the steps below to create a GitLab repository and commit a simple Go web application to it.

1. Log in to GitLab using the GitLab CLI:

  ```shell
  glab auth login -h gitlab.com
  ```

1. Create a directory and module for the Go application:

  ```shell
  mkdir myapp
  cd myapp
  go mod init main
  ```

1. Install the Echo web framework:

  ```shell
  go get github.com/labstack/echo/v4
  ```

1. Create a file named `server.go` and add the following code to it to create a skeleton application:

  ```go
  package main

  import (
    "net/http"

    "github.com/labstack/echo/v4"
  )

  func main() {
    e := echo.New()
    e.GET("/", func(c echo.Context) error {
      return c.String(http.StatusOK, "Hello, Dagger!")
    })
    e.Logger.Fatal(e.Start(":1323"))
  }
  ```

1. Create a private repository in your GitLab account:

  ```shell
  glab repo create myapp
  ```

1. Commit and push the application code:

  ```shell
  git add .
  git commit -a -m "Initial commit"
  git push --set-upstream origin master
  ```

## Appendix B: Configure a self-hosted GitLab Runner for use with Dagger

This tutorial assumes that you have a GitLab Runner application to run your GitLab CI/CD pipelines. This could be either a GitLab-managed runner or a self-hosted runner. [Learn about GitLab Runner](https://docs.gitlab.com/runner/).

To use GitLab's managed runners, you must [associate a valid credit card with your GitLab account](https://about.gitlab.com/pricing/#why-do-i-need-to-enter-credit-debit-card-details-for-free-pipeline-minutes). Alternatively, you can configure a self-hosted runner on your local host by following the steps below.

1. [Install GitLab Runner](https://docs.gitlab.com/runner/install/index.html) for your host's operating system.
1. Navigate to the `Settings` -> `CI/CD` -> `Runners` page in the GitLab Web interface.
1. Disable shared runners by unchecking the `Enable shared runners for this project` option.

  ![Disable shared runners](/img/current/guides/gitlab-google-cloud/gitlab-disable-shared-runners.png)

1. Copy the project-specific registration token, as shown below:

  ![Runner registration token](/img/current/guides/gitlab-google-cloud/gitlab-self-hosted-runner-token.png)

1. On your local host, register the runner using the command below. Replace the TOKEN placeholder with the registration token.

  ```shell
  sudo gitlab-runner register -n \
    --name dagger \
    --url https://gitlab.com/ \
    --executor docker \
    --docker-privileged \
    --docker-volumes /cache \
    --docker-volumes /certs/client \
    --docker-image docker:20.10.16 \
    --registration-token TOKEN
  ```

1. Navigate to the `Settings` -> `CI/CD` -> `Runners` page in the GitLab Web interface. Confirm that the newly-registered runner is active for the project, as shown below:

  ![Runner registration](/img/current/guides/gitlab-google-cloud/gitlab-self-hosted-runner-active.png)
