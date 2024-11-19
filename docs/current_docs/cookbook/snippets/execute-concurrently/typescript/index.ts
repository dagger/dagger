import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  source: Directory

  constructor(source: Directory) {
    this.source = source
  }

  /**
   * Return the result of running unit tests
   */
  @func()
  async test(): Promise<string> {
    return this.buildEnv().withExec(["npm", "run", "test:unit", "run"]).stdout()
  }

  /**
   * Return the result of running the linter
   */
  @func()
  async lint(): Promise<string> {
    return this.buildEnv().withExec(["npm", "run", "lint"]).stdout()
  }

  /**
   * Return the result of running the type-checker
   */
  @func()
  async typecheck(): Promise<string> {
    return this.buildEnv().withExec(["npm", "run", "type-check"]).stdout()
  }

  /**
   * Run linter, type-checker, unit tests concurrently
   */
  @func()
  async runAllTests(): Promise<void> {
    await Promise.all([this.test(), this.lint(), this.typecheck()])
  }

  /**
   * Build a ready-to-use development environment
   */
  @func()
  buildEnv(): Container {
    const nodeCache = dag.cacheVolume("node")
    return dag
      .container()
      .from("node:21-slim")
      .withDirectory("/src", this.source)
      .withMountedCache("/root/.npm", nodeCache)
      .withWorkdir("/src")
      .withExec(["npm", "install"])
  }
}
