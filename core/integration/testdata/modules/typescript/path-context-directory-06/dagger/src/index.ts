import { Directory, File, object, func, argument } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  async dirs(
    @argument({ defaultPath: "/" }) root: Directory,
    @argument({ defaultPath: "." }) relativeRoot: Directory,
  ): Promise<string[]> {
    const res = await root.entries()
    const relativeRes = await relativeRoot.entries()

    return [...res, ...relativeRes]
  }

  @func()
  async rootDirPath(
    @argument({ defaultPath: "/backend" }) backend: Directory,
    @argument({ defaultPath: "/frontend" }) frontend: Directory,
    @argument({ defaultPath: "/dagger/sub" }) modSrcDir: Directory,
  ): Promise<string[]> {
    const backendFiles = await backend.entries()
    const frontendFiles = await frontend.entries()
    const modSrcDirFiles = await modSrcDir.entries()

    return [...backendFiles, ...frontendFiles, ...modSrcDirFiles]
  }

  @func()
  async relativeDirPath(
    @argument({ defaultPath: "./dagger/sub" }) modSrcDir: Directory,
    @argument({ defaultPath: "./backend" }) backend: Directory,
  ): Promise<string[]> {
    const modSrcDirFiles = await modSrcDir.entries()
    const backendFiles = await backend.entries()

    return [...modSrcDirFiles, ...backendFiles]
  }

	  @func()
	  async files(
	    @argument({ defaultPath: "/LICENSE" }) license: File,
	    @argument({ defaultPath: "./dagger.json" }) daggerConfig: File,
	  ): Promise<string[]> {
    return [await license.name(), await daggerConfig.name()]
  }
}
