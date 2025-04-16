import { dag, object, func, Container } from "@dagger.io/dagger"

@object()
export class CodingAgent {
  /**
   * Write a Go program
   */
  @func()
  goProgram(
    /**
     * The programming assignment, e.g. "write me a curl clone"
     */
    assignment: string,
  ): Container {
    const environment = dag
      .env()
      .withStringInput("assignment", assignment, "the assignment to complete")
      .withContainerInput(
        "builder",
        dag.container().from("golang").withWorkdir("/app"),
        "a container to use for building Go code",
      )
      .withContainerOutput(
        "completed",
        "the completed assignment in the Golang container",
      )

    const work = dag
      .llm()
      .withEnv(environment)
      .withPrompt(
        `You are an expert Go programmer with an assignment to create a Go program
        Create files in the default directory in $builder
        Always build the code to make sure it is valid
        Do not stop until your assignment is completed and the code builds
        Your assignment is: $assignment`,
      )

    return work.env().output("completed").asContainer()
  }
}
