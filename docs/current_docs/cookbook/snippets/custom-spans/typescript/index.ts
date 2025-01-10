import { dag, Container, object, func } from "@dagger.io/dagger"
import * as trace from "@dagger.io/dagger/telemetry"

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

    // run tasks concurrently
    // emit a span for each
    await Promise.all([
      this.lint(container),
      this.typecheck(container),
      this.test(container),
    ])
  }

  private async lint(container: Container): Promise<void> {
    const tracer = trace.getTracer(MyModule.name)
    await tracer.startActiveSpan("lint code", async () => {
      const result = await container.withExec(["npm", "run", "lint"]).sync()
    })
  }

  private async typecheck(container: Container): Promise<void> {
    const tracer = trace.getTracer(MyModule.name)
    await tracer.startActiveSpan("check types", async () => {
      const result = await container
        .withExec(["npm", "run", "type-check"])
        .sync()
    })
  }

  private async test(container: Container): Promise<void> {
    const tracer = trace.getTracer(MyModule.name)
    await tracer.startActiveSpan("run unit tests", async () => {
      const result = await container
        .withExec(["npm", "run", "test:unit", "run"])
        .sync()
    })
  }
}
