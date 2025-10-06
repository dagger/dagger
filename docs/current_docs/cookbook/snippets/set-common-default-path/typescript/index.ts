import { dag, Directory, object, func, argument } from "@dagger.io/dagger"

@object()
class MyModule {
  source: Directory

  constructor(
    @argument({ defaultPath: "." })
    source: Directory,
  ) {
    this.source = source
  }

  @func()
  async foo(): Promise<string[]> {
    return await dag
      .container()
      .from("alpine:latest")
      .withMountedDirectory("/app", this.source)
      .directory("/app")
      .entries()
  }
}
