import { dag, object, Directory, Container, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async foo(): Promise<string> {
    return await dag
      .container()
      .from("alpine:latest")
      .withExec(["sh", "-c", "echo hello world > /foo"])
      .withExec(["cat", "/FOO"]) // deliberate error
      .stdout()
  }
}

// run with dagger call --interactive foo
