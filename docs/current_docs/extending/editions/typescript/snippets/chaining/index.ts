import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async foo(): Promise<string> {
    return await dag
      .container()
      .from("alpine:latest")
      .withEntrypoint(["cat", "/etc/os-release"])
      .publish("ttl.sh/my-alpine")
  }
}
