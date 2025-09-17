import { dag, object, func, File } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async readFile(source: File): Promise<string> {
    return await dag
      .container()
      .from("alpine:latest")
      .withFile("/src/myfile", source)
      .withExec(["cat", "/src/myfile"])
      .stdout()
  }
}
