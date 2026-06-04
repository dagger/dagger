import {
  dag,
  Directory,
  Container,
  File,
  object,
  func,
} from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Return a directory
   */
  @func()
  getDir(): Directory {
    return this.base().directory("/src")
  }

  /**
   * Return a file
   */
  @func()
  getFile(): File {
    return this.base().file("/src/foo")
  }

  /**
   * Return a base container
   */
  @func()
  base(): Container {
    return dag
      .container()
      .from("alpine:latest")
      .withExec(["mkdir", "/src"])
      .withExec(["touch", "/src/foo", "/src/bar"])
  }
}
