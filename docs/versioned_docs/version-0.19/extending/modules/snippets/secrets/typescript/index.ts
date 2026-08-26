import { dag, object, func, Secret } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async showSecret(token: Secret): Promise<string> {
    return await dag
      .container()
      .from("alpine:latest")
      .withSecretVariable("MY_SECRET", token)
      .withExec(["sh", "-c", `echo this is the secret: $MY_SECRET`])
      .stdout()
  }
}
