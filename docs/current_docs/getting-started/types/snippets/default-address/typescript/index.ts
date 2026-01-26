import { dag, Container, object, func, argument } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async version(
    @argument({ defaultAddress: "alpine:latest" })
    ctr: Container
  ): Promise<string> {
    return ctr.withExec(["cat", "/etc/alpine-release"]).stdout()
  }
}
