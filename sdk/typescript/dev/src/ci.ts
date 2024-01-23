import { Container, dag, func, object } from "@dagger.io/dagger"

@object
export class CI {
  ctr: Container

  constructor(ctr: Container) {
    this.ctr = ctr
  }

  /**
   * Run the CI (lint, test, build).
   */
  @func
  async run(): Promise<void> {
    await Promise.all([
      await this.lint(),
      await this.test(),
      await this.build().stdout(),
    ])
  }

  /**
   * Execute the TypeScript SDK unit tests.
   *
   * Example usage: `dagger call ci test`
   */
  @func
  async test(): Promise<string> {
    // We cannot use node module here because the tests
    // need access to experimental dagger.
    // TODO: fix provisioning tests (that will be outdated with 0.10??)
    return this.ctr
      .withExec(["test"], { experimentalPrivilegedNesting: true })
      .stdout()
  }

  /**
   * Lint the TypeScript SDK.
   *
   * Example usage: `dagger call ci lint`
   */
  @func
  async lint(): Promise<string> {
    return dag.node().withContainer(this.ctr).commands().lint()
  }

  /**
   * Build the TypeScript SDK.
   *
   * Example usage `dagger call -o ./dist ci build directory --path dist`
   */
  @func
  build(): Container {
    return dag.node().withContainer(this.ctr).commands().build()
  }
}
