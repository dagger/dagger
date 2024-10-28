import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  foo(source: Directory): Container {
    const builder = dag
      .container()
      .from("golang:latest")
      .withDirectory("/src", source, { exclude: ["*.git", "internal"] })
      .withWorkdir("/src/hello")
      .withExec(["go", "build", "-o", "hello.bin", "."])

    return dag
      .container()
      .from("alpine:latest")
      .withDirectory("/app", builder.directory("/src/hello"), {
        include: ["hello.bin"],
      })
      .withEntrypoint(["/app/hello.bin"])
  }
}
