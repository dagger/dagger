---
slug: /183109/aws-lambda
displayed_sidebar: "current"
category: "guides"
tags: ["nodejs", "go", "python", "aws-lambda", "aws"]
authors: ["Vikram Vaswani"]
date: "2023-06-27"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Deploy AWS Lambda Functions with Dagger

## Introduction

This tutorial teaches you how to create a local Dagger pipeline to update and deploy an existing AWS Lambda function using a ZIP archive.

## Requirements

This tutorial assumes that:

- You have a basic understanding of the JavaScript programming language.
- You have a basic understanding of the AWS Lambda service. If not, learn about [AWS Lambda](https://docs.aws.amazon.com/lambda/latest/dg/welcome.html).
- You have a Go, Node.js or Python development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have the Dagger CLI installed in your development environment. If not, [install the Dagger CLI](../cli/465058-install.md).
- You have an AWS account with appropriate privileges to create and manage AWS Lambda resources. If not, [register for an AWS account](https://aws.amazon.com/).
- You have an existing AWS Lambda function with a publicly-accessible URL in Go, Node.js or Python, deployed as a ZIP archive. If not, follow the steps in Appendix A to [create an example AWS Lambda function](#appendix-a-create-an-example-aws-lambda-function).

## Step 1: Create a Dagger pipeline

The first step is to create a Dagger pipeline to build a ZIP archive of the function and deploy it to AWS Lambda.

<Tabs groupId="language">
<TabItem value="Go">

1. In the function directory, install the Dagger SDK:

  ```shell
  go get dagger.io/dagger
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.go` and add the following code to it.

  ```go file=./snippets/aws-lambda/main.go
  ```

  This file performs the following operations:
    - It imports the Dagger SDK.
    - It checks for AWS credentials and configuration in the host environment.
    - It creates a Dagger client with `Connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `SetSecret()` method to set the AWS credentials as secrets for the Dagger pipeline.
    - It uses the client's `Host().Directory()` method to obtain a reference to the current directory on the host, excluding the `ci` directory. This reference is stored in the `source` variable.
    - It uses the client's `Container().From()` method to initialize a new container image from a base  `node:18-alpine` image. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `WithDirectory()` method to return the container image with the host directory written at the `/src` path, and the `WithWorkdir()` method to set the working directory in the container image.
    - It chains together a series of `WithExec()` method calls to install dependencies and build a ZIP deployment archive containing the function and all its dependencies.
    - It uses the client's `Container().From()` method to initialize a new `aws-cli` AWS CLI container image.
    - It uses the `Container` object's `WithSecretVariable()` and `WithEnvVariable()` methods to inject the AWS credentials (as secrets) and configuration into the container environment, so that they can be used by the AWS CLI.
    - It copies the ZIP archive containing the new AWS Lambda function code from the previous `node:18-alpine` container image into the `aws-cli` container image.
    - It uses `WithExec()` method calls to execute AWS CLI commands in the container image to upload and deploy the ZIP archive and get the function's public URL. If these operations complete successfully, it prints a success message with the URL to the console.

1. Run the following command to update `go.sum`:

  ```shell
  go mod tidy
  ```

</TabItem>
<TabItem value="Node.js">

1. In the function directory, install the Dagger SDK:

  ```shell
  npm install @dagger.io/dagger@latest --save-dev
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `index.mjs` and add the following code to it.

  ```javascript file=./snippets/aws-lambda/index.mjs
  ```

  This file performs the following operations:
    - It imports the Dagger SDK.
    - It checks for AWS credentials and configuration in the host environment.
    - It creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `setSecret()` method to set the AWS credentials as secrets for the Dagger pipeline.
    - It uses the client's `host().directory()` method to obtain a reference to the current directory on the host, excluding the `node_modules` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `container().from()` method to initialize a new container image from a base  `node:18-alpine` image. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `withDirectory()` method to return the container image with the host directory written at the `/src` path, and the `withWorkdir()` method to set the working directory in the container image.
    - It chains together a series of `withExec()` method calls to install dependencies and build a ZIP deployment archive containing the function and all its dependencies.
    - It uses the client's `container().from()` method to initialize a new `aws-cli` AWS CLI container image.
    - It uses the `Container` object's `withSecretVariable()` and `withEnvVariable()` methods to inject the AWS credentials (as secrets) and configuration into the container environment, so that they can be used by the AWS CLI.
    - It copies the ZIP archive containing the new AWS Lambda function code from the previous `node:18-alpine` container image into the `aws-cli` container image.
    - It uses `withExec()` method calls to execute AWS CLI commands in the container image to upload and deploy the ZIP archive and get the function's public URL. If these operations complete successfully, it prints a success message with the URL to the console.

</TabItem>
<TabItem value="Python">

1. In the function directory, create a virtual environment and install the Dagger SDK:

  ```shell
  pip install dagger-io
  ```

1. Create a new sub-directory named `ci`. Within the `ci` directory, create a file named `main.py` and add the following code to it.

  ```python file=./snippets/aws-lambda/main.py
  ```

  This file performs the following operations:
    - It imports the Dagger SDK.
    - It checks for AWS credentials and configuration in the host environment.
    - It creates a Dagger client with `dagger.Connection()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `set_secret()` method to set the AWS credentials as secrets for the Dagger pipeline.
    - It uses the client's `host().directory()` method to obtain a reference to the current directory on the host, excluding the `packages`, `.venv` and `ci` directories. This reference is stored in the `source` variable.
    - It uses the client's `container().from_()` method to initialize a new container image from a base  `node:18-alpine` image. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `with_directory()` method to mount the host directory into the container image at the `/src` mount point, and the `with_workdir()` method to set the working directory in the container image.
    - It chains together a series of `with_exec()` method calls to install dependencies and build a ZIP deployment archive containing the function and all its dependencies.
    - It uses the client's `container().from_()` method to initialize a new `aws-cli` AWS CLI container image.
    - It uses the `Container` object's `with_secret_variable()` and `with_env_variable()` methods to inject the AWS credentials (as secrets) and configuration into the container environment, so that they can be used by the AWS CLI.
    - It copies the ZIP archive containing the new AWS Lambda function code from the previous `node:18-alpine` container image into the `aws-cli` container image.
    - It uses `with_exec()` method calls to execute AWS CLI commands in the container image to upload and deploy the ZIP archive and get the function's public URL. If these operations complete successfully, it prints a success message with the URL to the console.

</TabItem>
</Tabs>

:::tip
Most `Container` object methods return a revised `Container` object representing the new state of the container. This makes it easy to chain methods together. Dagger evaluates pipelines "lazily", so the chained operations are only executed when required - in this case, when the container is published. Learn more about [lazy evaluation in Dagger](../api/975146-concepts.mdx#lazy-evaluation).
:::

## Step 2: Test the Dagger pipeline

Configure the credentials and default region for the AWS CLI as environment variables on the local host by executing the commands below. Replace the `KEY-ID` and `SECRET` placeholders with the AWS access key and secret respectively, and the `REGION` placeholder with the default AWS region.

```shell
export AWS_ACCESS_KEY_ID=KEY-ID
export AWS_SECRET_ACCESS_KEY=SECRET
export AWS_DEFAULT_REGION=REGION
```

Once the AWS CLI environment variables are set, you're ready to test the Dagger pipeline. Do so by making a change to the function and then executing the pipeline to update and deploy the revised function on AWS Lambda.

If you are using the example application function in [Appendix A](#appendix-a-create-an-example-aws-lambda-function), the following command modifies the function code to display a list of commits (instead of issues) from the Dagger GitHub repository:

```shell
sed -i -e 's|/dagger/issues|/dagger/commits|g' lambda.py
```

After modifying the function code, execute the Dagger pipeline:

<Tabs groupId="language">
<TabItem value="Go">

```shell
dagger run go run ci/main.go
```

</TabItem>
<TabItem value="Node.js">

```shell
dagger run node ci/index.mjs
```

</TabItem>
<TabItem value="Python">

```shell
dagger run python ci/main.py
```

</TabItem>
</Tabs>

Dagger performs the operations defined in the pipeline script, logging each operation to the console. At the end of the process, the ZIP archive containing the revised function code is deployed to AWS Lambda and a message similar to the one below appears in the console output:

```shell
Function updated at: https://...
```

Browse to the public URL endpoint displayed in the output to verify the output of the revised AWS Lambda function.

## Conclusion

This tutorial walked you through the process of creating a local Dagger pipeline to update and deploy a function on AWS Lambda. It used the Dagger SDKs and explained key concepts, objects and methods available in the SDKs to construct a Dagger pipeline.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.

## Appendix A: Create an example AWS Lambda function

This tutorial assumes that you have an AWS Lambda function written in Go, Node.js or Python and configured with a publicly-accessible URL. If not, follow the steps below to create an example function.

:::info
This section assumes that you have the AWS CLI and a GitHub personal access token. If not, [install the AWS CLI](https://aws.amazon.com/cli/), learn how to [configure the AWS CLI](https://docs.aws.amazon.com/cli/latest/userguide/cli-chap-configure.html) and learn how to [obtain a GitHub personal access token](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/creating-a-personal-access-token).
:::

1. Create a service role for AWS Lambda executions:

  ```shell
  aws iam create-role --role-name my-lambda-role --assume-role-policy-document '{"Version": "2012-10-17","Statement": [{ "Effect": "Allow", "Principal": {"Service": "lambda.amazonaws.com"}, "Action": "sts:AssumeRole"}]}'
  aws iam attach-role-policy --role-name my-lambda-role --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole
  ```

  Note the role ARN (the `Role.Arn` field) in the output of the first command, as you will need it in subsequent steps.

1. Create a directory named `myfunction` for the function code.

    ```shell
    mkdir myfunction
    cd myfunction
    ```

  <Tabs groupId="language">
  <TabItem value="Go">

  Within that directory, run the following commands to create a new Go module and add dependencies:

  ```shell
  go mod init main
  go get github.com/aws/aws-lambda-go/lambda
  ```

  Within the same directory, create a file named `lambda.go` and fill it with the following code:

  ```go file=./snippets/aws-lambda/lambda.go
  ```

  Build the function:

  ```shell
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o lambda lambda.go
  ```

  </TabItem>
  <TabItem value="Node.js">

  Within that directory, run the following commands to initialize a new Node.js project and add dependencies:

  ```shell
  npm init -y
  npm install node-fetch
  ```

  Within the same directory, create a file named `lambda.js` and fill it with the following code:

  ```javascript file=./snippets/aws-lambda/lambda.js
  ```

  </TabItem>
  <TabItem value="Python">

  Within that directory, run the following commands to install project dependencies and create a requirements file:

  ```shell
  pip install --target ./packages requests
  pip freeze --path ./packages > requirements.txt
  ```

  Within the same directory, create a file named `lambda.py` and fill it with the following code:

  ```python file=./snippets/aws-lambda/lambda.py
  ```

  </TabItem>
  </Tabs>

  This simple function performs an HTTP request to the GitHub API to return a list of issues from the Dagger GitHub repository. It expects to find a GitHub personal access token in the function environment and it uses this token for request authentication.

1. Deploy the function to AWS Lambda. Replace the `ROLE-ARN` placeholder with the service role ARN obtained previously and the `TOKEN` placeholder with your GitHub API token.

  <Tabs groupId="language">
  <TabItem value="Go">

  ```shell
  zip function.zip lambda
  aws lambda create-function --function-name myFunction --zip-file fileb://function.zip --runtime go1.x --handler lambda --timeout 10 --role ROLE-ARN
  aws lambda update-function-configuration --function-name myFunction --environment Variables={GITHUB_API_TOKEN=TOKEN}
  aws lambda add-permission --function-name myFunction --statement-id FunctionURLAllowPublicAccess --action lambda:InvokeFunctionUrl --principal "*" --function-url-auth-type NONE
  aws lambda create-function-url-config --function-name myFunction --auth-type NONE
  ```

  </TabItem>
  <TabItem value="Node.js">

  ```shell
  zip -p -r function.zip .
  aws lambda create-function --function-name myFunction --zip-file fileb://function.zip --runtime nodejs18.x --handler lambda.handler --timeout 10 --role ROLE-ARN
  aws lambda update-function-configuration --function-name myFunction --environment Variables={GITHUB_API_TOKEN=TOKEN}
  aws lambda add-permission --function-name myFunction --statement-id FunctionURLAllowPublicAccess --action lambda:InvokeFunctionUrl --principal "*" --function-url-auth-type NONE
  aws lambda create-function-url-config --function-name myFunction --auth-type NONE
  ```

  </TabItem>
  <TabItem value="Python">

  ```shell
  cd packages
  zip -r ../function.zip .
  cd ..
  zip function.zip lambda.py
  aws lambda create-function --function-name myFunction --zip-file fileb:///tmp/function.zip --runtime python3.10 --handler lambda.handler --timeout 10 --role ROLE-ARN
  aws lambda update-function-configuration --function-name myFunction --environment Variables={GITHUB_API_TOKEN=TOKEN}
  aws lambda add-permission --function-name myFunction --statement-id FunctionURLAllowPublicAccess --action lambda:InvokeFunctionUrl --principal "*" --function-url-auth-type NONE
  aws lambda create-function-url-config --function-name myFunction --auth-type NONE
  ```

  </TabItem>
  </Tabs>

  This sequence of commands creates a ZIP deployment archive, deploys it as a new AWS Lambda function named `myFunction`, and creates a publicly-accessible URL endpoint. The public URL endpoint is listed in the output of the last command.

1. Browse to the public URL endpoint to test the AWS Lambda function. Confirm that it displays a JSON-encoded list of issues from the Dagger GitHub repository.
