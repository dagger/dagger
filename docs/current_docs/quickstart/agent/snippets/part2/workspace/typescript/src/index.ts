import { dag, object, func, Directory } from "@dagger.io/dagger"

@object()
export class Workspace {
  source: Directory

  constructor(source: Directory) {
    this.source = source
  }

  @func()
  static new(
    source: Directory,
  ): Workspace {
    return new Workspace(source)
  }

  @func()
  async readFile(
    path: string,
  ): Promise<string> {
    return await this.source.file(path).contents()
  }

  @func()
  writeFile(
    path: string,
    contents: string,
  ): Workspace {
    this.source = this.source.withNewFile(path, contents)
    return this
  }

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

  @func()
  getSource(): Directory {
    return this.source
  }
}
