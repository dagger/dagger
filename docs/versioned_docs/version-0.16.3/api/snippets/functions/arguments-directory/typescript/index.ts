import { dag, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async tree(src: Directory, depth: string): Promise<string> {
    return await dag
      .container()
      .from("alpine:latest")
      .withMountedDirectory("/mnt", src)
      .withWorkdir("/mnt")
      .withExec(["apk", "add", "tree"])
      .withExec(["tree", "-L", depth])
      .stdout()
  }
}
