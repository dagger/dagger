# Dagger Examples

If you'd like to see a new example, please open an issue.

## Core concepts

### Mounting source files to a container

- [Go](./sdk/go/db-service/main.go#L37)
- [NodeJS](./sdk/nodejs/db-service/build.js#L19)
- [Python](./sdk/python/db-service/pipeline.py#L28)

### Multi-stage build

- [Go](./sdk/go/multistage/main.go#L31)
- [NodeJS](./sdk/nodejs/multistage/build.js#L14)
- [Python](./sdk/python/multistage/pipeline.py#L19)

### Multi-platform build

- [Go](./sdk/go/multiplatform/main.go#L30)
- [NodeJS](./sdk/nodejs/multiplatform/build.js#L16)
- [Python](./sdk/python/multiplatform/pipeline.py#L19)

### Cache mounts

- [Go](./sdk/go/multiplatform/main.go#L34)
- [NodeJS](./sdk/nodejs/multiplatform/build.js#L20)
- [Python](./sdk/python/multiplatform/pipeline.py#L23)

### Concurrency

Note: While the DAG is constructed serially, the engine will execute the full DAG when the build artifacts directory is exported. Since each platform's build doesn't depend on the others, the engine will execute each build concurrently.

- [Go](./sdk/go/multiplatform/main.go#L29)
- [NodeJS](./sdk/nodejs/multiplatform/build.js#L15)
- [Python](./sdk/python/multiplatform/pipeline.py#L17)

### Container publishing

- [Go](./sdk/go/multistage/main.go#L41)
- [NodeJS](./sdk/nodejs/multistage/build.js#L23)
- [Python](./sdk/python/multistage/pipeline.py#L30)

### Secrets

- [Go](./sdk/go/secrets/main.go#L21)
- [NodeJS](./sdk/nodejs/secrets/ci.js#L6)
- [Python](./sdk/python/secrets/pipeline.py#L9)

### Services

- [Go](./sdk/go/db-service/main.go#L22)
- [NodeJS](./sdk/nodejs/db-service/build.js#L6)
- [Python](./sdk/python/db-service/pipeline.py#L10)

## By language

### Go

- [multiarch build](./sdk/go/multiarch-build/)
- [npm build](./sdk/go/npm-build/)
- [yarn build](./sdk/go/yarn-build/)
- [gradle build](./sdk/go/gradle-build/)
- [services](./sdk/go/db-service/)
- [secrets](./sdk/go/secrets/)
- [multiplatform](./sdk/go/multiplatform/)
- [multistage](./sdk/go/multistage/)
- [replace dockerfile](./sdk/go/replace-dockerfile/)
- [aws cdk](./sdk/go/aws-cdk/)

### Python

- [basic example](./sdk/python/basic-example/)
- [services](./sdk/python/db-service/)
- [secrets](./sdk/python/secrets/)
- [multiplatform](./sdk/python/multiplatform/)
- [multistage](./sdk/python/multistage/)

### Node.js (Typescript/Javascript)

- [react build](./sdk/nodejs/react-build/)
- [services](./sdk/nodejs/db-service/)
- [secrets](./sdk/nodejs/secrets/)
- [multiplatform](./sdk/nodejs/multiplatform/)
- [multistage](./sdk/nodejs/multistage/)
- [Pulumi](./sdk/nodejs/pulumi/)

### Bash with Dagger CLI and GraphQL

- [git build](./cli/git-build/)
