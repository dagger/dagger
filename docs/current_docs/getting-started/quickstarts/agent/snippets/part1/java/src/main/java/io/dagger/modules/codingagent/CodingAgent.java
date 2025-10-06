package io.dagger.modules.codingagent;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.Env;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

/** CodingAgent main object */
@Object
public class CodingAgent {
  /** Write a Go program */
  @Function
  public Container goProgram(String assignment) {
    Env environment = dag().env()
        .withStringInput("assignment", assignment, "the assignment to complete")
        .withContainerInput("builder", dag().container().from("golang").withWorkdir("/app"), "a container to use for building Go code")
        .withContainerOutput("completed", "the completed assignment in the Golang container");
    return dag()
      .llm()
      .withEnv(environment)
      .withPrompt("""
        You are an expert Go programmer with an assignment to create a Go program
        Create files in the default directory in $builder
        Always build the code to make sure it is valid
        Do not stop until your assignment is completed and the code builds
        Your assignment is: $assignment
        """)
      .env()
      .output("completed")
      .asContainer();
  }
}
