import { dag, object, func, File } from "@dagger.io/dagger"

@object()
export class MyModule {
  @func()
  agent(): File {
    const dir = dag.git("github.com/golang/example").branch("master").tree()
    const builder = dag.container().from("golang:latest")

    const environment = dag
      .env()
      .withContainerInput("container", builder, "a Golang container")
      .withDirectoryInput("directory", dir, "a directory with source code")
      .withFileOutput("file", "the built Go executable")

    const work = dag
      .llm()
      .withEnv(environment)
      .withPrompt(
        `You have access to a Golang container.
        You also have access to a directory containing Go source code.
        Mount the directory into the container and build the Go application.
        Once complete, return only the built binary.`,
      )

    return work.env().output("file").asFile()
  }
}
