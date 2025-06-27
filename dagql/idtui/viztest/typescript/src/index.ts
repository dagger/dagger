import { dag, Container, object, func } from "@dagger.io/dagger"
import { getTracer } from "@dagger.io/dagger/telemetry"

let now = new Date().toISOString()

@object()
export class Typescript {
  @func()
  echo(msg: string): Promise<string> {
    return dag
      .container()
      .from("alpine:latest")
      .withExec(["echo", msg])
      .stdout()
  }

  @func()
  async pending(): Promise<void> {
    await dag
      .container()
      .from("alpine:latest")
      .withEnvVariable("NOW", now)
      .withExec(["sleep", "1"])
      .withExec(["false"])
      .withExec(["sleep", "1"])
      .sync()
  }

  @func()
  async failLog(): Promise<void> {
    await dag
      .container()
      .from("alpine")
      .withEnvVariable("NOW", now)
      .withExec([
        "sh",
        "-c",
        "echo im doing a lot of work; echo and then failing; exit 1",
      ])
      .sync()
  }

  @func()
  async failLogNative(): Promise<void> {
    console.log("im doing a lot of work")
    console.log("and then failing")
    throw new Error("i failed")
  }

  @func()
  failEffect(): Container {
    return dag
      .container()
      .from("alpine")
      .withExec(["sh", "-c", "echo this is a failing effect; exit 1"])
  }

  @func()
  async customStatus(): Promise<string> {
    return dag.status("custom status").run(async () => {
      return this.echo(`hello from TypeScript! it is currently ${now}`)
    })
  }

  @func()
  async nestedStatuses(fail = false): Promise<string> {
    return dag.status("custom status").run(async () => {
      await this.echo(`outer: ${now}`)

      // First sub-status
      await dag.status("sub status").run(async () => {
        await this.echo(`sub 1: ${now}`)
      })

      // Second sub-status
      await dag.status("sub status").run(async () => {
        await this.echo(`sub 2: ${now}`)
      })

      // Nested sub-status
      await dag.status("another sub status").run(async () => {
        await dag.status("sub status").run(async () => {
          if (fail) {
            throw new Error("oh no")
          } else {
            await this.echo(`im even deeper: ${now}`)
          }
        })
      })

      return "done"
    })
  }
}
