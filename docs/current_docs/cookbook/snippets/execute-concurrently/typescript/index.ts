import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Return the result of running unit tests
   */
  @func()
  async test(source: Directory): Promise<string> {
    return this.buildEnv(source)
      .withExec(["npm", "run", "test:unit", "run"])
      .stdout()
  }

  /**
   * Return the result of running the linter
   */
  @func()
  async lint(source: Directory): Promise<string> {
    return this.buildEnv(source).withExec(["npm", "run", "lint"]).stdout()
  }

  /**
   * Return the result of running the type-checker
   */
  @func()
  async typecheck(source: Directory): Promise<string> {
    return this.buildEnv(source).withExec(["npm", "run", "typecheck"]).stdout()
  }

  /**
   * Run linter, type-checker, unit tests concurrently
   */
  @func()
  async runAllTests(source: Directory): Promise<string> {
    const results = await Promise.all([
      this.lint(source),
      this.typecheck(source),
      this.test(source),
    ])
    return results.join("\n")
  }

  /**
   * Build a ready-to-use development environment
   */
  @func()
  buildEnv(source: Directory): Container {
    const nodeCache = dag.cacheVolume("node")
    return dag
      .container()
      .from("node:21-slim")
      .withDirectory("/src", source)
      .withMountedCache("/root/.npm", nodeCache)
      .withWorkdir("/src")
      .withExec(["npm", "install"])
  }
}
