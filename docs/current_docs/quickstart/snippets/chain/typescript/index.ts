import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class HelloDagger {
  @func()
  foo(): Directory {
    return dag.container().from("cgr.dev/chainguard/wolfi-base").directory("/")
  }

  @func()
  async bar(): Promise<string[]> {
    return await this.foo().entries()
  }
}
