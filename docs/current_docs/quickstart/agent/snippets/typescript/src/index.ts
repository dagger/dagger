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
    const workspace = dag.toyWorkspace()
    const environment = dag
      .env()
      .withToyWorkspaceInput(
        "before",
        workspace,
        "tools to complete the assignment",
      )
      .withStringInput("assignment", assignment, "the assignment to complete")
      .withToyWorkspaceOutput("after", "the completed assignment")

    return dag
      .llm()
      .withEnv(environment)
      .withPrompt(`
        You are an expert go programmer. You have access to a workspace.
  			Use the default directory in the workspace.
  			Do not stop until the code builds.
  			Your assignment is: $assignment
			`)
      .env()
      .output("after")
      .asToyWorkspace()
      .container()
  }
}
