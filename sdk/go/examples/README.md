# Dagger Go SDK Examples

If you'd like to see a new example, please open an issue.

## Core concepts

- [Mounting source files to a container](./db-service/main.go#L37)
- [Multi-stage build](./multistage/main.go#L31)
- [Multi-platform build](./multiplatform/main.go#L30)
- [Cache mounts](./multiplatform/main.go#L34)
- [Concurrency](./multiplatform/main.go#L29)[^1]
- [Container publishing](./multistage/main.go#L41)
- [Secrets](./secrets/main.go#L21)
- [Services](./db-service/main.go#L22)

[^1]: While the DAG is constructed serially, the engine will execute the full DAG when the build artifacts directory is exported. Since each platform's build doesn't depend on the others, the engine will execute each build concurrently.

## Complete examples

- [Multi-architecture build](./multiarch-build/)
- [npm build](./npm-build/)
- [yarn build](./yarn-build/)
- [gradle build](./gradle-build/)
- [Services](./db-service/)
- [Secrets](./secrets/)
- [Multi-platform build](./multiplatform/)
- [Multi-stage build](./multistage/)
- [Replace Dockerfile](./replace-dockerfile/)
- [AWS CDK](./aws-cdk/)
