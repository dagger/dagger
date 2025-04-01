package io.dagger.modules.codingagent;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.ToyWorkspace;
import io.dagger.client.Env;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

/** CodingAgent main object */
@Object
public class CodingAgent {
  /** Write a Go program */
  @Function
  public Container goProgram(String assignment) {
    ToyWorkspace workspace = dag.toyWorkspace();
    Env environment = dag.env()
        .withToyWorkspaceInput("before", workspace, "these are the tools to complete the task")
        .withStringInput("assignment", assignment, "this is the assignment, complete it")
        .withStringOutput("after", "the ToyWorkspace with the completed assignment");
    return dag()
      .llm()
      .withEnv(environment)
      .withPrompt("""
        You are an expert go programmer. You have access to a workspace.
        Use the default directory in the workspace.
        Do not stop until the code builds.
        """)
      .env()
      .output("after")
      .asToyWorkspace()
      .container();
  }
}
