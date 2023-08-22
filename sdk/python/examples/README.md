# Dagger Python SDK Examples

If you'd like to see a new example, please open an issue.

## Core concepts

- [Mounting source files to a container](./db-service/pipeline.py#L28)
- [Multi-stage build](./multistage/pipeline.py#L19)
- [Multi-platform build](./multiplatform/pipeline.py#L19)
- [Cache mounts](./multiplatform/pipeline.py#L23)
- [Concurrency](./multiplatform/pipeline.py#L17)[^1]
- [Container publishing](./multistage/pipeline.py#L30)
- [Secrets](./secrets/pipeline.py#L9)
- [Services](./db-service/pipeline.py#L10)

[^1]: While the DAG is constructed serially, the engine will execute the full DAG when the build artifacts directory is exported. Since each platform's build doesn't depend on the others, the engine will execute each build concurrently.

## Complete examples

- [Basic example](./basic-example/)
- [Services](./db-service/)
- [Secrets](./secrets/)
- [Multi-platform build](./multiplatform/)
- [Multi-stage build](./multistage/)
