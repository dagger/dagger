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
  async pending(): Promise<void> {
    await dag.container().
      from("alpine:latest").
      withEnvVariable("NOW", now).
      withExec(["sleep", "1"]).
      withExec(["false"]).
      withExec(["sleep", "1"]).
      sync()
  }

  @func()
  async customSpan(): Promise<string> {
    return dag.span("custom span").run(async () => {
      return this.echo(`hello from TypeScript! it is currently ${now}`)
    })
  }

  @func()
  async exceptionalSpan(): Promise<string> {
    return dag.span("custom span").run(async () => {
      throw new Error("oh no");
    });
  }

  @func()
  async nestedSpans(): Promise<string> {
    return dag.span("custom span").run(async () => {
      await this.echo("outer");

      // First sub-span
      await dag.span("sub span").run(async () => {
        await this.echo("sub 1");
      });

      // Second sub-span
      await dag.span("sub span").run(async () => {
        await this.echo("sub 2");
      });

      // Nested sub-span
      await dag.span("another sub span").run(async () => {
        await dag.span("sub span").run(async () => {
          await this.echo("im even deeper");
        });
      });

      return "done";
    });
  }
}
