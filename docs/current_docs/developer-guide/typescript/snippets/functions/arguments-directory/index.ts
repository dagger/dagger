import { dag, Directory, object, func } from "@dagger.io/dagger";

@object()
class MyModule {
  @func()
  async tree(dir: Directory, depth: string): Promise<string> {
    return await dag
      .container()
      .from("alpine:latest")
      .withMountedDirectory("/mnt", dir)
      .withWorkdir("/mnt")
      .withExec(["apk", "add", "tree"])
      .withExec(["tree", "-L", depth])
      .stdout();
  }
}
