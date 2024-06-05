import { dag, object, func, Secret } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Query the GitHub API
   */
  @func()
  async githubApi(
    /**
     * GitHub Hosts configuration File
     */
    file: Secret,
  ): Promise<string> {
    return await dag
      .container()
      .from("alpine:3.17")
      .withExec(["apk", "add", "github-cli"])
      .withSecretVariable("/root/.config/gh/hosts.yml", file)
      .withExec(["gh", "auth", "status"])
      .stdout()
  }
}
