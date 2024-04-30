---
slug: /integrations/573829/java
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Java

Dagger can be used to perform common CI tasks - testing, containerizing, publishing and more - for any Java application.

The following code sample demonstrates how these CI tasks can be encapsulated as Dagger Functions in a Dagger module. It assumes:

- A Spring Boot Web application with Maven for dependency management
- Credentials to publish the containerized application image to a registry

<Tabs groupId="language">
<TabItem value="Go">

Bootstrap a new module:

```shell
dagger init --name=my-module --sdk=go
```

Install the Java module from the Daggerverse:

```shell
dagger install github.com/jcsirot/daggerverse/java@c591e9e0b99def2fc8a67bb090fca5cd06cf6a1d
```

Update the generated `dagger/main.go` file with the following code:

```go file=./snippets/java/go/main.go
```

</TabItem>
<TabItem value="Python">

Bootstrap a new module:

```shell
dagger init --name=my-module --sdk=python
```

Install the Java module from the Daggerverse:

```shell
dagger install github.com/jcsirot/daggerverse/java@c591e9e0b99def2fc8a67bb090fca5cd06cf6a1d
```

Update the generated `dagger/src/main/__init__.py` file with the following code:

```python file=./snippets/java/python/main.py
```

</TabItem>
<TabItem value="TypeScript">

Bootstrap a new module:

```shell
dagger init --name=my-module --sdk=typescript
```

Install the Java module from the Daggerverse:

```shell
dagger install github.com/jcsirot/daggerverse/java@c591e9e0b99def2fc8a67bb090fca5cd06cf6a1d
```

Update the generated `dagger/src/index.ts` file with the following code:

```typescript file=./snippets/java/typescript/index.ts
```

</TabItem>
</Tabs>

:::warning
The code sample above is illustrative only. Modify it to your application's specific requirements.
:::

Here is an example of calling the Dagger Function to publish the application image to Docker Hub. Replace the `DOCKER-HUB-USERNAME` and `DOCKER-HUB-PASSWORD` placeholders with your Docker Hub credentials.

```shell
export REGISTRY_PASSWORD=DOCKER-HUB-PASSWORD
dagger call publish \
  --source=. \
  --version=0.1 \
  --registry-address=docker.io \
  --registry-username=DOCKER-HUB-USERNAME \
  --registry-password=env:REGISTRY_PASSWORD \
  --image-name=my-java-app
```
