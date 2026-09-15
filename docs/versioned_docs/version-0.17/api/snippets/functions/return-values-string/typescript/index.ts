import { Container, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async osInfo(ctr: Container): Promise<string> {
    return ctr.withExec(["uname", "-a"]).stdout()
  }
}
