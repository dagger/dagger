import { dag, object, func, Directory } from "@dagger.io/dagger"

@object()
export class Workspace {
  /**
   * the workspace source code
   */
  @func()
  source: Directory

  constructor(source: Directory) {
    this.source = source
  }

  /**
   * Read a file in the Workspace
   *
   * @param path The path to the file in the workspace
   */
  @func()
  async readFile(path: string): Promise<string> {
    return await this.source.file(path).contents()
  }

  /**
   * Write a file to the Workspace
   *
   * @param path The path to the file in the workspace
   * @param contents The new contents of the file
   */
  @func()
  writeFile(path: string, contents: string): Workspace {
    this.source = this.source.withNewFile(path, contents)
    return this
  }

  /**
   * List all of the files in the Workspace
   */
  @func()
  async listFiles(): Promise<string> {
    return await dag
      .container()
      .from("alpine:3")
      .withDirectory("/src", this.source)
      .withWorkdir("/src")
      .withExec(["tree", "./src"])
      .stdout()
  }

  /**
   * Return the result of running unit tests
   */
  @func()
  async test(): Promise<string> {
    const nodeCache = dag.cacheVolume("node")
    return dag
      .container()
      .from("node:21-slim")
      .withDirectory("/src", this.source)
      .withMountedCache("/root/.npm", nodeCache)
      .withWorkdir("/src")
      .withExec(["npm", "install"])
      .withExec(["npm", "run", "test:unit", "run"])
      .stdout()
  }
}
