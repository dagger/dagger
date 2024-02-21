import { dag, object, func } from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {

  @func()
  async getUser(gender: string): Promise<string> {
    return await dag.container()
      .from("alpine:latest")
      .withExec(["apk", "add", "curl"])
      .withExec(["apk", "add", "jq"])
      .withExec(["sh", "-c", `curl https://randomuser.me/api/?gender=${gender} | jq .results[0].name`])
      .stdout()
  }

}
