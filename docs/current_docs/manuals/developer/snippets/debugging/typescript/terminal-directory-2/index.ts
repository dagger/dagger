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
        container: dag.container().from("ubuntu"),
        cmd: ["/bin/bash"],
      })
      .file("README.md")
      .contents()
  }
}
