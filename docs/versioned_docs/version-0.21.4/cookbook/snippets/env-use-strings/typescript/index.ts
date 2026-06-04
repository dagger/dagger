import { dag, object, func } from "@dagger.io/dagger"

@object()
export class MyModule {
  @func()
  async agent(question: string): Promise<string> {
    const environment = dag
      .env()
      .withStringInput("question", question, "the question")
      .withStringOutput("answer", "the answer to the question")

    const work = dag
      .llm()
      .withEnv(environment)
      .withPrompt(
        `You are an assistant that helps with complex questions.
        You will receive a question and you need to provide a detailed answer.
        Make sure to use the provided context and environment variables.
        Your answer should be clear and concise.
        Your question is: $question`,
      )

    return await work.env().output("answer").asString()
  }
}
