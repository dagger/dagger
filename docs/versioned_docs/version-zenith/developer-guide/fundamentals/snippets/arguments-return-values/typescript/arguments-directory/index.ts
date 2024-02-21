import { dag, Directory, object, func } from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {

  @func()
  async tree(dir: Directory): Promise<string> {
    return await dag.container()
      .from("alpine:latest")
      .withMountedDirectory("/mnt", dir)
      .withWorkdir("/mnt")
      .withExec(["tree"])
      .stdout()
  }

}
