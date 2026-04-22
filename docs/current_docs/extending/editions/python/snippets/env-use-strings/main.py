from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def agent(self, question: str) -> str:
        environment = (
            dag.env()
            .with_string_input("question", question, "the question")
            .with_string_output("answer", "the answer to the question")
        )

        work = (
            dag.llm()
            .with_env(environment)
            .with_prompt(
                """
                You are an assistant that helps with complex questions.
                You will receive a question and you need to provide a detailed answer.
                Make sure to use the provided context and environment variables.
                Your answer should be clear and concise.
                Your question is: $question
                """
            )
        )

        return await work.env().output("answer").as_string()
