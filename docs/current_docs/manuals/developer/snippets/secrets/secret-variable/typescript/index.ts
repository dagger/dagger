import { dag, object, func, Secret } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async githubAauth(secret: Secret): Promise<string> {
    return await dag
      .container()
      .from("alpine:3.17")
      .withExec(["apk", "add", "github-cli"])
      .withMountedSecret("/root/.config/gh/hosts.yml", secret)
      .withWorkdir("/root")
      .withExec(["gh", "auth", "status"])
      .stdout()
  }
}
