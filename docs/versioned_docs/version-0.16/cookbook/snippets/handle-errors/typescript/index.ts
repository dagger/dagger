import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Generate an error
   */
  @func()
  async test(): Promise<string> {
    try {
      return await dag
        .container()
        .from("alpine")
        // ERROR: cat: read error: Is a directory
        .withExec(["cat", "/"])
        .stdout()
    } catch (e) {
      return `Test pipeline failure: ${e.stderr}`
    }
  }
}
