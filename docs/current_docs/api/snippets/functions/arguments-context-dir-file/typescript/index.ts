import { Directory, File, argument, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async readDir(
    @argument({ defaultPath: "/" }) source: Directory,
  ): Promise<string[]> {
    return await source.entries()
  }

  @func()
  async readFile(
    @argument({ defaultPath: "/README.md" }) source: File,
  ): Promise<string> {
    return await source.contents()
  }
}
