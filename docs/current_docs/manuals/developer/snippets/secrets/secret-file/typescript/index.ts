import { dag, object, func, Secret } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async githubApi(file: Secret): Promise<string> {
    return await dag
      .container()
      .from("alpine:3.17")
      .withExec(["apk", "add", "github-cli"])
      .withSecretVariable("/root/.config/gh/hosts.yml", file)
      .withExec(["gh", "auth", "status"])
      .stdout()
  }
}
