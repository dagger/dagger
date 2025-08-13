import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def agent(self) -> dagger.Container:
        base = dag.container().from_("alpine:latest")
        environment = (
            dag.env()
            .with_container_input("base", base, "a base container to use")
            .with_container_output("result", "the updated container")
        )

        work = (
            dag.llm()
            .with_env(environment)
            .with_prompt(
                """
                You are a software engineer with deep knowledge of Web application
                development.
                You have access to a container.
                Install the necessary tools and libraries to create a
                complete development environment for Web applications.
                Once complete, return the updated container.
                """
            )
        )

        return work.env().output("result").as_container()
