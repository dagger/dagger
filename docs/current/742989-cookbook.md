---
slug: /7442989/cookbook
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Cookbook

## Filesystem operations

### Set the host working directory

The following code listing sets the host working directory for the Dagger pipeline.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/set-workdir/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Obtain a reference to a host directory

The following code listing obtains a reference to the host working directory.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Obtain a reference to a host directory with specific files excluded by pattern

The following code listing obtains a reference to the host working directory containing all files except `*.txt` files.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir-exclude/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Obtain a reference to a host directory with specific files included by pattern

The following code listing obtains a reference to the host working directory containing only `*.rar` files.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir-include/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Obtain a reference to a host directory with combined exclusion and inclusion patterns

The following code listing obtains a reference to the host working directory containing all files except `*.rar` files.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/list-dir-exclude-include/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Mount a host directory in a container

The following code listing mounts a host directory in a container at the `/host` container path and then executes a command in the container referencing the mounted directory.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/mount-dir/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Export a directory from a container to the host

The following code listing creates a file in a container's `/tmp` directory and then exports the contents of that directory to the host's temporary directory.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/work-with-host-filesystem/export-dir/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/421437-work-with-host-filesystem.md)

### Export a container from a Dagger pipeline to the host

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

### Perform a multi-stage build

The following code listing performs a multi-stage build.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./guides/snippets/multistage-build/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/544174-multistage-build.md)

### Build images for multiple OSs and CPU architectures

The following code listing builds separate images for multiple OS and CPU architecture combinations.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

```go file=./snippets/multi-builds/main.go
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

### Build a single multi-architecture image using emulation

The following code listing builds a single image for different CPU architectures using native emulation.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

```go file=./snippets/multiplatform-support/build-images-emulation/main.go
```

</TabItem>
</Tabs>

[Learn more](./guides/406009-multiplatform-support.md)

### Build a single multi-architecture image using cross-compilation

The following code listing builds a single image for different CPU architectures using cross-compilation.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

```go file=./snippets/multiplatform-support/build-images-cross-compilation/main.go
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
