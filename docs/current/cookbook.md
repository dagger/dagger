---
slug: /cookbook
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Cookbook

## Filesystem

### Add Git repository to container

The following code listing adds a remote Git repository branch to a container at the `/src` container path and then executes a command in the container referencing the repository.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/filesystem-operations/add-git-dir/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/filesystem-operations/add-git-dir/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/filesystem-operations/add-git-dir/main.py
```

</TabItem>
</Tabs>

### List host directory contents

The following code listing obtains a reference to the host working directory and lists the directory's contents.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./guides/snippets/work-with-host-filesystem/list-dir/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/work-with-host-filesystem/list-dir/main.py
```

</TabItem>
</Tabs>

When the Dagger pipeline code is in a sub-directory, it may be more useful to set the parent directory (the project's root directory) as the working directory.

The following listing revises the previous one, obtaining a reference to the parent directory on the host and listing its contents.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir-parent/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./guides/snippets/work-with-host-filesystem/list-dir-parent/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/work-with-host-filesystem/list-dir-parent/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Mount host directory in container

The following code listing mounts a host directory in a container at the `/host` container path and then executes a command in the container referencing the mounted directory.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/mount-dir/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./guides/snippets/work-with-host-filesystem/mount-dir/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/work-with-host-filesystem/mount-dir/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Get host directory with filters

The following code listing obtains a reference to the host working directory containing all files except `*.txt` files.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir-exclude/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./guides/snippets/work-with-host-filesystem/list-dir-exclude/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/work-with-host-filesystem/list-dir-exclude/main.py
```

</TabItem>
</Tabs>

The following code listing obtains a reference to the host working directory containing only `*.rar` files.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir-include/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./guides/snippets/work-with-host-filesystem/list-dir-include/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/work-with-host-filesystem/list-dir-include/main.py
```

</TabItem>
</Tabs>

The following code listing obtains a reference to the host working directory containing all files except `*.rar` files.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir-exclude-include/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./guides/snippets/work-with-host-filesystem/list-dir-exclude-include/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/work-with-host-filesystem/list-dir-exclude-include/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Add Git repository as directory to container

The following code listing adds a remote Git repository branch to a container as a directory at the `/src` container path and then executes a command in the container to list the directory contents.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/filesystem-operations/add-git-dir/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/filesystem-operations/add-git-dir/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/filesystem-operations/add-git-dir/main.py
```

</TabItem>
</Tabs>

### Add Git repository as directory to container with filters

The following code listing adds a remote Git repository branch as a directory at the `/src` container path, excluding `*.md` files.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/filesystem-operations/add-git-dir-exclude/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/filesystem-operations/add-git-dir-exclude/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/filesystem-operations/add-git-dir-exclude/main.py
```

</TabItem>
</Tabs>

The following code listing adds a remote Git repository branch as a directory at the `/src` container path, including only `*.md` files.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/filesystem-operations/add-git-dir-include/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/filesystem-operations/add-git-dir-include/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/filesystem-operations/add-git-dir-include/main.py
```

</TabItem>
</Tabs>

The following code listing adds a remote Git repository branch as a directory at the `/src` container path, including all files except files beginning with `.git`.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/filesystem-operations/add-git-dir-exclude-include/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/filesystem-operations/add-git-dir-exclude-include/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/filesystem-operations/add-git-dir-exclude-include/main.py
```

</TabItem>
</Tabs>

## Builds

### Perform multi-stage build

The following code listing performs a multi-stage build.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/multistage-build/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./guides/snippets/multistage-build/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/multistage-build/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/544174-multistage-build.md)

### Perform matrix build

The following code listing builds separate images for multiple OS and CPU architecture combinations.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

```go file=./guides/snippets/multi-builds/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./guides/snippets/multi-builds/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/multi-builds/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/648384-multi-builds.md)

### Build multi-arch image

The following code listing builds a single image for different CPU architectures using native emulation.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

```go file=./guides/snippets/multiplatform-support/build-images-emulation/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/406009-multiplatform-support.md)

### Build multi-arch image with cross-compilation

The following code listing builds a single image for different CPU architectures using cross-compilation.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

```go file=./guides/snippets/multiplatform-support/build-images-cross-compilation/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/406009-multiplatform-support.md)

### Build image from Dockerfile

The following code listing builds an image using an existing Dockerfile.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./quickstart/snippets/build-dockerfile/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./quickstart/snippets/build-dockerfile/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./quickstart/snippets/build-dockerfile/main.py
```

</TabItem>
</Tabs>

[Learn more](./quickstart/429462-build-dockerfile.mdx)

### Add OCI annotations to image

The following code listing adds [OpenContainer Initiative (OCI) annotations](https://github.com/opencontainers/image-spec/blob/v1.0/annotations.md) to an image.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/oci-annotations/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/oci-annotations/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/oci-annotations/main.py
```

</TabItem>
</Tabs>

### Define build-time variables

The following code listing defines various environment variables for build purposes.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

```go file=./guides/snippets/multi-builds/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./guides/snippets/multi-builds/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/multi-builds/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/648384-multi-builds.md)

### Access private Git repository

The following code listing demonstrates how to access a private Git repository using SSH.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/private-repositories/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./guides/snippets/private-repositories/clone.ts
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/private-repositories/clone.py
```

</TabItem>
</Tabs>

### Use transient database for application tests

The following code listing creates a temporary MariaDB database service and binds it to an application container for unit/integration testing.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/use-services/use-db-service/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./guides/snippets/use-services/use-db-service/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/use-services/use-db-service/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/757394-use-service-containers.md)

### Invalidate cache

The following code listing demonstrates how to invalidate the Dagger cache and thereby force execution of subsequent pipeline steps, by introducing a volatile time variable at a specific point in the Dagger pipeline.

:::note
This is a temporary workaround until cache invalidation support is officially added to Dagger.
:::

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/cache-invalidation/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/cache-invalidation/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/cache-invalidation/main.py
```

</TabItem>
</Tabs>

## Outputs

### Publish image to registry

The following code listing publishes a container image to a remote registry (Docker Hub). Replace the `DOCKER-HUB-USERNAME` and `DOCKER-HUB-PASSWORD` placeholders with your Docker Hub username and password respectively.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/use-secrets/sdk/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./guides/snippets/use-secrets/sdk/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/use-secrets/sdk/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/723462-use-secrets.md)

### Export image to host

The following code listing exports a container image from a Dagger pipeline to the host.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/load-images-local-docker-engine/export/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./guides/snippets/load-images-local-docker-engine/export/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/load-images-local-docker-engine/export/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/252029-load-images-local-docker-engine.md)

### Export container directory to host

The following code listing exports the contents of a container directory to the host's temporary directory.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/export-dir/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Publish image to registry with multiple tags

The following code listing tags a container image multiple times and publishes it to a remote registry (Docker Hub). Set the Docker Hub username and password as host environment variables named `DOCKERHUB_USERNAME` and `DOCKERHUB_PASSWORD` respectively.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/multiple-tags/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/multiple-tags/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/multiple-tags/main.py
```

</TabItem>
</Tabs>

## Secrets

### Expose secret via environment variable

The following code listing demonstrates how to inject an environment variable in a container as a secret.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/use-secrets/host-env/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./guides/snippets/use-secrets/host-env/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/use-secrets/host-env/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/723462-use-secrets.md)

### Expose secret via file

The following code listing demonstrates how to inject a file in a container as a secret.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/use-secrets/host-fs/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./guides/snippets/use-secrets/host-fs/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/use-secrets/host-fs/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/723462-use-secrets.md)

### Load secret from Google Cloud Secret Manager

The following code listing reads a secret (a GitHub API token) from Google Cloud Secret Manager and uses it in a Dagger pipeline to interact with the GitHub API.

Set up [Application Default Credentials (ADC)](https://cloud.google.com/docs/authentication/provide-credentials-adc) and replace the `PROJECT-ID` and `SECRET-ID` placeholders with your Google Cloud project and secret identifiers respectively.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/use-secrets/external/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./guides/snippets/use-secrets/external/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/use-secrets/external/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/723462-use-secrets.md)

### Load secret from Hashicorp Vault

The following code listing reads a secret (a GitHub API token) from a Hashicorp Vault Key/Value v2 engine and uses it in a Dagger pipeline to interact with the GitHub API.

Set the Hashicorp Vault URI, namespace, role and secret identifiers as host environment variables named `VAULT_ADDRESS`, `VAULT_NAMESPACE`, `VAULT_ROLE_ID` and `VAULT_SECRET_ID` respectively. Replace the `MOUNT-PATH`, `SECRET-ID` and `SECRET-KEY` placeholders with your Hashicorp Vault mount point, secret identifier and key respectively.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/secrets-vault/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/secrets-vault/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/secrets-vault/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/723462-use-secrets.md)

### Mount directories as secrets in a container

The following code listing demonstrates how to securely mount directories as secrets in a container. The directory structure/file names will be accessible, but contents of the secrets will be scrubbed:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/mount-directories-as-secrets/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/mount-directories-as-secrets/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/mount-directories-as-secrets/main.py
```

</TabItem>
</Tabs>

## Error handling

### Terminate gracefully

The following code listing demonstrates how to handle errors gracefully, without crashing the program or script running Dagger pipelines.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/error-handling/aborting/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/error-handling/aborting/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/error-handling/aborting/main.py
```

</TabItem>
</Tabs>

### Handle exit code and unexpected errors

The following code listing demonstrates how to handle a non-zero exit code (an error from running a command) in a container, with several use cases:

- Difference between “test failed” and “failed to test”
- Handle a specific exit code value
- Handle a failure from a command executed in a container, without checking for the exit code
- Catching and handling a failure from a command executed in a container, without propagating it
- Get the standard output of a command, irrespective of whether or not it failed

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/error-handling/exit-code/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/error-handling/exit-code/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/error-handling/exit-code/main.py
```

</TabItem>
</Tabs>

### Continue using container after command execution fails

This code listing demonstrates how to continue using a container after a command executed within it fails. A common use case for this is to export a report that a test suite tool generates.

:::note
The caveat with this approach is that forcing a zero exit code on a failure caches the failure. This may not be desired depending on the use case.
:::

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/error-handling/postmortem/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/error-handling/postmortem/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/error-handling/postmortem/main.py
```

</TabItem>
</Tabs>

## Optimizations

### Cache dependencies

The following code listing uses a cache volume for application dependencies. This enables Dagger to reuse the contents of the cache every time the pipeline runs, and thereby speed up pipeline operations.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./quickstart/snippets/caching/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./quickstart/snippets/caching/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./quickstart/snippets/caching/main.py
```

</TabItem>
</Tabs>

[Learn more](./quickstart/635927-caching.mdx)

### Persist service state between runs

The following code listing uses a cache volume to persist a service's data across pipeline runs.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/use-services/persist-service-state/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./guides/snippets/use-services/persist-service-state/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/use-services/persist-service-state/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/757394-use-service-containers.md)

### Add multiple environment variables to a container

The following code listing demonstrates how to add multiple environment variables to a container.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/environment-variables/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./cookbook/snippets/environment-variables/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./cookbook/snippets/environment-variables/main.py
```

</TabItem>
</Tabs>

## Integrations

### Docker Engine

The following code shows different ways to integrate with the Docker Engine.

#### Connecting to Docker Engine on the host

This shows how to connect to a Docker Engine on the host machine, by mounting the Docker unix socket into a container, and running the `docker` CLI.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./cookbook/snippets/docker-engine-host/main.go
```

</TabItem>

<TabItem value="Node.js">

```javascript file=./cookbook/snippets/docker-engine-host/index.mjs
```

</TabItem>

<TabItem value="Python">

```python file=./cookbook/snippets/docker-engine-host/main.py
```

</TabItem>

</Tabs>

### AWS Cloud Development Kit

The following code listing builds, publishes and deploys a container using the Amazon Web Services (AWS) Cloud Development Kit (CDK).

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/aws-cdk-ecs/main.go
```

```go file=./guides/snippets/aws-cdk-ecs/aws.go
```

```go file=./guides/snippets/aws-cdk-ecs/registry.go
```

</TabItem>
</Tabs>

[Learn more](./guides/899944-aws-cdk-ecs.md)

### Google Cloud Run

The following code listing builds, publishes and deploys a container using Google Container Registry and Google Cloud Run.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/github-google-cloud/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./guides/snippets/github-google-cloud/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/github-google-cloud/main.py
```

</TabItem>
</Tabs>

[Learn more](./guides/620941-github-google-cloud.md)

### GitHub Actions

<Tabs groupId="language">
<TabItem value="Go">

The following code listing shows how to integrate Dagger with GitHub Actions.

```yaml title=".github/workflows/dagger.yml" file=./guides/snippets/ci/go/actions.yml
```

</TabItem>
<TabItem value="Node.js">

```yaml title=".github/workflows/dagger.yaml" file=./guides/snippets/ci/nodejs/actions.yml
```

</TabItem>
<TabItem value="Python">

```yaml title=".github/workflows/dagger.yaml" file=./guides/snippets/ci/python/actions.yml
```

</TabItem>
</Tabs>

[Learn more](./guides/145912-ci.md)

### GitLab CI

The following code listing shows how to integrate Dagger with GitLab CI.

<Tabs groupId="language">
<TabItem value="Go">

```yaml title=".gitlab-ci.yml" file=./guides/snippets/ci/go/gitlab.yml
```

</TabItem>
<TabItem value="Node.js">

```yaml title=".gitlab-ci.yml" file=./guides/snippets/ci/nodejs/gitlab.yml
```

</TabItem>
<TabItem value="Python">

```yaml title=".gitlab-ci.yml" file=./guides/snippets/ci/python/gitlab.yml
```

</TabItem>
</Tabs>

[Learn more](./guides/145912-ci.md)

### CircleCI

The following code listing shows how to integrate Dagger with CircleCI.

<Tabs groupId="language">
<TabItem value="Go">

```yaml title=".circleci/config.yml" file=./guides/snippets/ci/go/circle.yml
```

</TabItem>
<TabItem value="Node.js">

```yaml title=".circleci/config.yml" file=./guides/snippets/ci/nodejs/circle.yml
```

</TabItem>
<TabItem value="Python">

```yaml title=".circleci/config.yml" file=./guides/snippets/ci/python/circle.yml
```

</TabItem>
</Tabs>

[Learn more](./guides/145912-ci.md)

### Jenkins

The following code listing shows how to integrate Dagger with Jenkins.

<Tabs groupId="language">
<TabItem value="Go">

```groovy title="Jenkinsfile" file=./guides/snippets/ci/go/Jenkinsfile
```

</TabItem>
<TabItem value="Node.js">

```groovy title="Jenkinsfile" file=./guides/snippets/ci/nodejs/Jenkinsfile
```

</TabItem>
<TabItem value="Python">

```groovy title="Jenkinsfile" file=./guides/snippets/ci/python/Jenkinsfile
```

</TabItem>
</Tabs>

Requires `docker` client and `go` installed on your Jenkins agent, a Docker host available (can be `docker:dind`), and agents labeled in Jenkins with `dagger`.

[Learn more](./guides/145912-ci.md)

### Azure Pipelines

The following code listing shows how to integrate Dagger with Azure Pipelines.

<Tabs groupId="language">
<TabItem value="Go">

```yaml title="azure-pipelines.yml" file=./guides/snippets/ci/go/azure-pipelines.yml
```

</TabItem>
<TabItem value="Node.js">

```yaml title="azure-pipelines.yml" file=./guides/snippets/ci/nodejs/azure-pipelines.yml
```

</TabItem>
<TabItem value="Python">

```yaml title="azure-pipelines.yml" file=./guides/snippets/ci/python/azure-pipelines.yml
```

</TabItem>
</Tabs>

[Learn more](./guides/145912-ci.md)

### AWS CodePipeline

The following code listing shows how to integrate Dagger with AWS CodePipeline.

<Tabs groupId="language">
<TabItem value="Go">

```yaml title="buildspec.yml" file=./guides/snippets/ci/go/buildspec.yml
```

</TabItem>
<TabItem value="Node.js">

```yaml title="buildspec.yml" file=./guides/snippets/ci/nodejs/buildspec.yml
```

</TabItem>
<TabItem value="Python">

```yaml title="buildspec.yml" file=./guides/snippets/ci/python/buildspec.yml
```

</TabItem>
</Tabs>

[Learn more](./guides/145912-ci.md)
