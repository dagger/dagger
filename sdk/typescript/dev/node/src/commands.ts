import { Container, object, func } from "@dagger.io/dagger"

@object
export class Commands {
  // Container to apply commands on
  ctr: Container

  constructor(ctr: Container) {
    this.ctr = ctr
  }

  @func
  run(args: string[]): Container {
    return this.ctr.withExec(["run", ...args])
  }

  @func
  async lint(): Promise<string> {
    return await this.run(["lint"]).stdout()
  }

  @func
  async test(): Promise<string> {
    return await this.run(["test"]).stdout()
  }

  @func
  build(): Container {
    return this.run(["build"])
  }
}
