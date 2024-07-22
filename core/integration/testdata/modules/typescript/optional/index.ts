import { dag, Directory, object, func } from "@dagger.io/dagger";

@object()
class Minimal {
  @func()
  src?: Directory;

  @func()
  name?: string;

  @func()
  isEmpty(): boolean {
    if (this.src !== undefined) {
      throw new Error(`src should be undefined but is ${this.src}`);
    }

    if (this.name !== undefined) {
      throw new Error(`name should be undefined but is ${this.name}`);
    }

    return true;
  }

  @func()
  foo(x?: string): string {
    if (x !== undefined) {
      throw new Error("uh oh");
    }

    return "";
  }

  @func()
  async resolveValue(dir: Directory = dag.directory().withNewFile("foo.txt", "hello world")): Promise<string> {
    return dir.file("foo.txt").contents();
  }
}
