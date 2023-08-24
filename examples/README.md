# Dagger Examples

If you'd like to see a new example, please open an issue.

## Core Concepts

### Mounting source files to a container

- [Go](./go/db-service/main.go#L37)
- [NodeJS](./nodejs/db-service/build.js#L19)
- [Python](./python/db-service/pipeline.py#L28)

### Multi-stage build

- [Go](./go/multistage/main.go#L31)
- [NodeJS](./nodejs/multistage/build.js#L14)
- [Python](./python/multistage/pipeline.py#L19)

### Multi-platform build

- [Go](./go/multiplatform/main.go#L30)
- [NodeJS](./nodejs/multiplatform/build.js#L16)
- [Python](./python/multiplatform/pipeline.py#L19)

### Cache mounts

- [Go](./go/multiplatform/main.go#L34)
- [NodeJS](./nodejs/multiplatform/build.js#L20)
- [Python](./python/multiplatform/pipeline.py#L23)

### Concurrency

Note: While the DAG is constructed serially, the engine will execute the full DAG when the build artifacts directory is exported. Since each platform's build doesn't depend on the others, the engine will execute each build concurrently.

- [Go](./go/multiplatform/main.go#L29)
- [NodeJS](./nodejs/multiplatform/build.js#L15)
- [Python](./python/multiplatform/pipeline.py#L17)

### Container publishing

- [Go](./go/multistage/main.go#L41)
- [NodeJS](./nodejs/multistage/build.js#L23)
- [Python](./python/multistage/pipeline.py#L30)

### Secrets

- [Go](./go/secrets/main.go#L21)
- [NodeJS](./nodejs/secrets/ci.js#L6)
- [Python](./python/secrets/pipeline.py#L9)

### Services

- [Go](./go/db-service/main.go#L22)
- [NodeJS](./nodejs/db-service/build.js#L6)
- [Python](./python/db-service/pipeline.py#L10)

### Testing your pipelines

- [Go]() TODO
- [NodeJS]() TODO
- [Python]() TODO

## By Language

### Go

- [multiarch build](./go/multiarch-build/)
- [npm build](./go/npm-build/)
- [yarn build](./go/yarn-build/)
- [gradle build](./go/gradle-build/)
- [services](./go/db-service/)
- [secrets](./go/secrets/)
- [multiplatform](./go/multiplatform/)
- [multistage](./go/multistage/)
- [replace dockerfile](./go/replace-dockerfile/)
- [aws cdk](./go/aws-cdk/)

### Python

- [basic example](./python/basic-example/)
- [services](./python/db-service/)
- [secrets](./python/secrets/)
- [multiplatform](./python/multiplatform/)
- [multistage](./python/multistage/)

### Node.js (Typescript/Javascript)

- [react build](./nodejs/react-build/)
- [services](./nodejs/db-service/)
- [secrets](./nodejs/secrets/)
- [multiplatform](./nodejs/multiplatform/)
- [multistage](./nodejs/multistage/)
- [Pulumi](./nodejs/pulumi/)

### Bash with Dagger CLI and GraphQL

- [git build](./bash/git-build/)

### GraphQL queries

- TODO. [API Reference here](https://docs.dagger.io/api)
