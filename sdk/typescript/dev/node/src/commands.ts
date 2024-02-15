import { Container, object, func } from "@dagger.io/dagger"

@object()
export class Commands {
  // Container to apply commands on
  ctr: Container

  constructor(ctr: Container) {
    this.ctr = ctr
  }

  @func()
  run(args: string[]): Container {
    return this.ctr.withExec(["run", ...args])
  }

  @func()
  lint(): Container {
    return this.run(["lint"])
  }

  @func()
  test(): Container {
    return this.run(["test"])
  }

  @func()
  build(): Container {
    return this.run(["build"])
  }
}
