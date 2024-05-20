import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class HelloDagger {
  /**
   * Returns the result of running unit tests
   */
  @func()
  async test(source: Directory): Promise<string> {
    // get the build environment container
    // by calling another Dagger Function
    return (
        this.buildEnv(source)
        // call the test runner
        .withExec(["npm", "run", "test:unit", "run"])
        // capture and return the command output
        .stdout()
    )
  }
}
