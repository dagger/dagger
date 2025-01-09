import { dag, Container, object, func } from "@dagger.io/dagger"
import { trace } from "@opentelemetry/api"

@object()
class MyModule {
  @func()
  async foo(): Promise<void> {
    // clone the source code repository
    const source = dag
      .git("https://github.com/dagger/hello-dagger")
      .branch("main")
      .tree()

    // set up a container with the source code mounted
    // install dependencies
    const container = dag
      .container()
      .from("node:latest")
      .withDirectory("/src", source)
      .withWorkdir("/src")
      .withExec(["npm", "install"])

    // run tasks in parallel
    // emit a span for each
    await Promise.all([
      await this.lint(container),
      await this.typecheck(container),
      await this.format(container),
      await this.test(container),
    ])
  }

  private async lint(container: Container): Promise<void> {
    const tracer = trace.getTracer("dagger-otel")
    const span = tracer.startSpan("lint code")
    try {
      const result = await container.withExec(["npm", "run", "lint"]).sync()
      if (await result.exitCode() !== 0) {
        throw new Error(`Linting failed with exit code ${result.exitCode}`)
      }
    } finally {
      span.end()
    }
  }

  private async typecheck(container: Container): Promise<void> {
    const tracer = trace.getTracer("dagger-otel")
    const span = tracer.startSpan("check types")
    try {
      const result = await container
        .withExec(["npm", "run", "type-check"])
        .sync()
      if (await result.exitCode() !== 0) {
        throw new Error(`Type check failed with exit code ${result.exitCode}`)
      }
    } finally {
      span.end()
    }
  }

  private async format(container: Container): Promise<void> {
    const tracer = trace.getTracer("dagger-otel")
    const span = tracer.startSpan("format code")
    try {
      const result = await container.withExec(["npm", "run", "format"]).sync()
      if (await result.exitCode() !== 0) {
        throw new Error(
          `Code formatting failed with exit code ${result.exitCode}`,
        )
      }
    } finally {
      span.end()
    }
  }

  private async test(container: Container): Promise<void> {
    const tracer = trace.getTracer("dagger-otel")
    const span = tracer.startSpan("run unit tests")
    try {
      const result = await container
        .withExec(["npm", "run", "test:unit", "run"])
        .sync()
      if (await result.exitCode() !== 0) {
        throw new Error(`Tests failed with exit code ${result.exitCode}`)
      }
    } finally {
      span.end()
    }
  }
}
