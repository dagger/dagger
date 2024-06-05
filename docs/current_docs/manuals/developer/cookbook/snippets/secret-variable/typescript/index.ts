import { dag, object, func, Secret } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Query the GitHub API
   */
  @func()
  async githubApi(
    /**
     * GitHub API token
     */
    token: Secret,
  ): Promise<string> {
    return await dag
      .container()
      .from("alpine:3.17")
      .withSecretVariable("GITHUB_API_TOKEN", token)
      .withExec(["apk", "add", "curl"])
      .withExec([
        "sh",
        "-c",
        `curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_API_TOKEN"`,
      ])
      .stdout()
  }
}
