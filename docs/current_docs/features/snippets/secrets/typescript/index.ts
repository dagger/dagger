import { dag, object, func, Secret } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
<<<<<<< HEAD
  async githubApi(token: Secret): Promise<string> {
=======
  async githubApi(
    token: Secret,
  ): Promise<string> {
>>>>>>> 81388975a (Added code snippets to feature pages)
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
