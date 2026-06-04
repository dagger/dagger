import { dag, Directory, File, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  archiver(src: Directory): File {
    return dag
      .container()
      .from("alpine:latest")
      .withExec(["apk", "add", "zip"])
      .withMountedDirectory("/src", src)
      .withWorkdir("/src")
      .withExec(["sh", "-c", "zip -p -r out.zip *.*"])
      .file("/src/out.zip")
  }
}
