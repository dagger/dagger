import { object, func, argument, Directory, File } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async repoFiles(
    @argument({ defaultPath: "/" }) repo: Directory,
  ): Promise<string[]> {
    return await repo.entries()
  }

  @func()
  async moduleFiles(
    @argument({ defaultPath: "." }) module: Directory,
  ): Promise<string[]> {
    return await module.entries()
  }

  @func()
  async readme(
    @argument({ defaultPath: "/README.md" }) readmeFile: File,
  ): Promise<string> {
    return await readmeFile.contents()
  }
}
