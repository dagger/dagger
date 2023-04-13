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

### Mount host directory in container

The following code listing mounts a host directory in a container at the `/host` container path and then executes a command in the container referencing the mounted directory.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/mount-dir/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Export directory from container to host

The following code listing creates a file in a container's `/tmp` directory and then exports the contents of that directory to the host's temporary directory.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/export-dir/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

## Container operations

### Publish container

The following code listing shows how to publish a container to a remote registry.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./quickstart/snippets/publish/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./quickstart/snippets/publish/index.mjs
```

</TabItem>
<TabItem value="Python">

```python file=./quickstart/snippets/publish/main.py
```

</TabItem>
</Tabs>

[Learn more](./quickstart/730264-quickstart-publish.mdx)

### Export container to host

The following code listing shows how to export a container from a Dagger pipeline to the host.

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

### Build multi-OS image

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

## Integrations

### Google Cloud

The following code listing builds, publishes and deploys a container using Google Container Registry and Google Cloud Run

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
