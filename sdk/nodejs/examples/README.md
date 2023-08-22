# Dagger Node.js SDK Examples

If you'd like to see a new example, please open an issue.

## Core concepts

- [Mounting source files to a container](./db-service/build.js#L19)
- [Multi-stage build](./multistage/build.js#L14)
- [Multi-platform build](./multiplatform/build.js#L16)
- [Cache mounts](./multiplatform/build.js#L20)
- [Concurrency](./multiplatform/build.js#L15)[^1]
- [Container publishing](./multistage/build.js#L23)
- [Secrets](./secrets/ci.js#L6)
- [Services](./db-service/build.js#L6)

[^1]: While the DAG is constructed serially, the engine will execute the full DAG when the build artifacts directory is exported. Since each platform's build doesn't depend on the others, the engine will execute each build concurrently.

## Complete examples

- [react build](./react-build/)
- [Services](./db-service/)
- [Secrets](./secrets/)
- [Multi-platform build](./multiplatform/)
- [Multi-stage build](./multistage/)
- [Pulumi](./pulumi/)
