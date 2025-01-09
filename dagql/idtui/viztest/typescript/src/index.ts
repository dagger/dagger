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
  async nestedSpans(fail = false): Promise<string> {
    return dag.span("custom span").run(async () => {
      await this.echo(`outer: ${now}`);

      // First sub-span
      await dag.span("sub span").run(async () => {
        await this.echo(`sub 1: ${now}`);
      });

      // Second sub-span
      await dag.span("sub span").run(async () => {
        await this.echo(`sub 2: ${now}`);
      });

      // Nested sub-span
      await dag.span("another sub span").run(async () => {
        await dag.span("sub span").run(async () => {
          if (fail) {
            throw new Error("oh no");
          } else {
            await this.echo(`im even deeper: ${now}`);
          }
        });
      });

      return "done";
    });
  }
}
