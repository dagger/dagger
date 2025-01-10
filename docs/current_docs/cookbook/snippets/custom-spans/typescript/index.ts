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

    // list versions to test against
    const versions = ["20", "22", "23"]

    const tracer = trace.getTracer(MyModule.name)

    // run tests concurrently
    // emit a span for each
    for (const version of versions) {
      await tracer.startActiveSpan(
        `running unit tests with Node ${version}`,
        async () => {
          await dag
            .container()
            .from(`node:${version}`)
            .withDirectory("/src", source)
            .withWorkdir("/src")
            .withExec(["npm", "install"])
            .withExec(["npm", "run", "test:unit", "run"])
            .sync()
        },
      )
    }
  }
}
