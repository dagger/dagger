import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async advancedDirectory(): Promise<string> {
    return await dag
      .git("https://github.com/dagger/dagger.git")
      .head()
      .tree()
      .terminal({
        cmd: ["/bin/bash"],
        experimentalPrivilegedNesting: false,
        insecureRootCapabilities: false,
      })
      .file("README.md")
      .contents()
  }
}
