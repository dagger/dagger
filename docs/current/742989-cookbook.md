---
slug: /7442989/cookbook
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Cookbook

## Filesystem operations

### Set host working directory

The following code listing sets the host working directory for the Dagger pipeline.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/set-workdir/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Get host directory

The following code listing obtains a reference to the host working directory.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir/main.go
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
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Get host directory with exclusions

The following code listing obtains a reference to the host working directory containing all files except `*.txt` files.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir-exclude/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Get host directory with inclusions

The following code listing obtains a reference to the host working directory containing only `*.rar` files.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir-include/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Get host directory with exclusions and inclusions

The following code listing obtains a reference to the host working directory containing all files except `*.rar` files.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir-exclude-include/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

## Build operations

### Perform multi-stage build

The following code listing performs a multi-stage build.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/multistage-build/main.go
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

### Build multi-architecture image (emulation)

The following code listing builds a single image for different CPU architectures using native emulation.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

```go file=./guides/snippets/multiplatform-support/build-images-emulation/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/406009-multiplatform-support.md)

### Build multi-architecture image (cross-compilation)

The following code listing builds a single image for different CPU architectures using cross-compilation.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

```go file=./guides/snippets/multiplatform-support/build-images-cross-compilation/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/406009-multiplatform-support.md)

### Access private Git repository

The following code listing demonstrates how to access a private Git repository using SSH.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/private-repositories/main.go
```

</TabItem>
<TabItem value="Node.js (TypeScript)">

```typescript file=./guides/snippets/private-repositories/clone.ts
```

</TabItem>
<TabItem value="Python">

```python file=./guides/snippets/private-repositories/clone.py
```

</TabItem>
</Tabs>

### Bind transient service

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

## Outputs

### Publish container

The following code listing publishes a container to a remote registry (Docker Hub). Replace the `DOCKER-HUB-USERNAME` and `DOCKER-HUB-PASSWORD` placeholders with your Docker Hub username and password respectively.

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

### Export container to host

The following code listing exports a container from a Dagger pipeline to the host.

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

### Export directory from container to host

The following code listing exports the contents of a directory to the host's temporary directory.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/export-dir/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

## Secrets

### Inject secret as environment variable

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

### Inject secret as file

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

[Learn more](./quickstart/635927-quickstart-caching.mdx)

### Cache service state

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

## Integrations

### Amazon Web Services

The following code listing builds, publishes and deploys a container using the AWS Cloud Development Kit (CDK).

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

### Google Cloud

#### Google Cloud Run

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

#### Google Cloud Secret Manager

The following code listing reads a secret from Google Cloud Secret Manager.

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
