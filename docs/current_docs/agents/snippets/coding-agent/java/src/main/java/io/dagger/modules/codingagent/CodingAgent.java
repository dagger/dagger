package io.dagger.modules.codingagent;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

/** CodingAgent main object */
@Object
public class CodingAgent {
  /** Write a Go program */
  @Function
  public Container goProgram(String assignment) {
    return dag()
      .llm()
      .withToyWorkspace(dag.toyWorkspace())
      .withPromptVar("assignment", assignment)
      .withPrompt("""
        You are an expert go programmer. You have access to a workspace.
        Use the default directory in the workspace.
        Do not stop until the code builds.
        Do not use the container.
        Complete the assignment: $assignment
        """)
      .toyWorkspace()
      .container();
  }
}
