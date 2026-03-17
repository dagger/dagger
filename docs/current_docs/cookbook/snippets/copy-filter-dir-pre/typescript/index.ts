import {
  dag,
  object,
  argument,
  func,
  Directory,
  Container,
} from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Return a container with a filtered directory
   */
  @func()
  async copy_directory_with_exclusions(
    @argument({ ignore: ["*", "!**/*.md"] }) source: Directory,
  ): Promise<Container> {
    return await dag
      .container()
      .from("alpine:latest")
      .withDirectory("/src", source)
      .sync()
  }
}
