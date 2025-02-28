import { dag, Container, func, object } from "@dagger.io/dagger";

@object()
export class ToyProgrammerTs {
  /**
   * Write a Go program based on the provided assignment.
   */
  @func()
  GoProgram(assignment: string): Container {
    // Create a new workspace using the third-party module
    let before = dag.toyWorkspace();

    // Run the agent loop in the workspace
    let after = dag
      .llm()
      .withToyWorkspace(before)
      .withPromptVar("assignment", assignment)
      .withPromptFile(dag.currentModule().source().file("prompt.txt"))
      .ToyWorkspace();

    // Return the modified workspace's container
    return after.container();
  }
}
