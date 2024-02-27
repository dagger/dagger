import { dag, object, func, Secret } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async githubApi(endpoint: string, token: Secret): Promise<string> {
    const plaintext = await token.plaintext()
    return await dag
      .container()
      .from("alpine:3.17")
      .withExec(["apk", "add", "curl"])
      .withExec([
        "sh",
        "-c",
        `curl "${endpoint}" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer ${plaintext}"`,
      ])
      .stdout()
  }
}
