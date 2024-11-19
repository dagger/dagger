import { dag, object, func } from "@dagger.io/dagger"
import { getTracer } from "@dagger.io/dagger/telemetry"

let now = new Date().toISOString()

@object()
export class Typescript {
  @func()
  echo(msg: string): Promise<string> {
    return dag.
      container().
      from("alpine:latest").
      withExec(["echo", msg]).
      stdout()
  }

  @func()
  async customSpan(): Promise<string> {
    return getTracer().startActiveSpan("custom span", async () => {
      return this.echo(`hello from TypeScript! it is currently ${now}`)
    })
  }

  @func()
  async pending(): Promise<void> {
    await dag.container().
      from("alpine:latest").
      withEnvVariable("NOW", now).
      withExec(["sleep", "1"]).
      withExec(["false"]).
      withExec(["sleep", "1"]).
      sync()
  }
}
