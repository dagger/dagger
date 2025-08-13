import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async simpleDirectory(): Promise<string> {
    return await dag
      .git("https://github.com/dagger/dagger.git")
      .head()
      .tree()
      .terminal()
      .file("README.md")
      .contents()
  }
}
