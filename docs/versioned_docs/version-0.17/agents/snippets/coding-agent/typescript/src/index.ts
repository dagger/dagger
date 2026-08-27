import { dag, Container, object, func } from "@dagger.io/dagger"

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
    const result = dag
      .llm()
      .withToyWorkspace(dag.toyWorkspace())
      .withPromptVar("assignment", assignment)
      .withPrompt(
        `
        You are an expert go programmer. You have access to a workspace.
        Use the default directory in the workspace.
        Do not stop until the code builds.
        Do not use the container.
        Complete the assignment: $assignment
        `,
      )
      .toyWorkspace()
      .container()
    return result
  }
}
