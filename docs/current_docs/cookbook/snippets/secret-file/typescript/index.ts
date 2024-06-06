import { dag, object, func, Secret } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Query the GitHub API
   */
  @func()
  async githubAuth(
    /**
     * GitHub Hosts configuration File
     */
    ghCreds: Secret,
  ): Promise<string> {
    return await dag
      .container()
      .from("alpine:3.17")
      .withExec(["apk", "add", "github-cli"])
      .withMountedSecret("/root/.config/gh/hosts.yml", ghCreds)
      .withExec(["gh", "auth", "status"])
      .stdout()
  }
}
