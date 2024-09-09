import { object, func, argument, Directory, File, field } from "@dagger.io/dagger"

@object()
class Files {
  @field()
  repoFiles: string[]

  @field()
  moduleFiles: string[]

  @field()
  readmeContent: string

  constructor(
    repoFiles: string[],
    moduleFiles: string[],
    readmeContent: string,
  ) {
    this.repoFiles = repoFiles
    this.moduleFiles = moduleFiles
    this.readmeContent = readmeContent
  }
}

@object()
class MyModule {
  @func()
  async repoFiles(
    @argument({ defaultPath: "/" }) repo: Directory,
    @argument({ defaultPath: "." }) moduleDir: Directory,
    @argument({ defaultPath: "/README.md" }) readmeFile: File,
  ): Promise<Files> {
    return new Files(
      await repo.entries(),
      await moduleDir.entries(),
      await readmeFile.contents(),
    )
  }
}
