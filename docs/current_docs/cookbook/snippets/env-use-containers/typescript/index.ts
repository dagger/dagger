import { dag, object, func, Container } from "@dagger.io/dagger"

@object()
export class MyModule {
  @func()
  agent(): Container {
    const base = dag.container().from("alpine:latest")
    const environment = dag
      .env()
      .withContainerInput("base", base, "a base container to use")
      .withContainerOutput("result", "the updated container")

    const work = dag
      .llm()
      .withEnv(environment)
      .withPrompt(
        `You are a software engineer with deep knowledge of Web application development.
        You have access to a container.
        Install the necessary tools and libraries to create a
        complete development environment for Web applications.
        Once complete, return the updated container.`,
      )

    return work.env().output("result").asContainer()
  }
}
