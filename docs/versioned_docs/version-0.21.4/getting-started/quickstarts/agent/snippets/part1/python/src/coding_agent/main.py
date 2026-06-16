import dagger
from dagger import dag, function, object_type


@object_type
class CodingAgent:
    @function
    def go_program(
        self,
        assignment: str,
    ) -> dagger.Container:
        """Write a Go program"""
        environment = (
            dag.env()
            .with_string_input("assignment", assignment, "the assignment to complete")
            .with_container_input(
                "builder",
                dag.container().from_("golang").with_workdir("/app"),
                "a container to use for building Go code",
            )
            .with_container_output(
                "completed", "the completed assignment in the Golang container"
            )
        )

        work = (
            dag.llm()
            .with_env(environment)
            .with_prompt(
                """
                You are an expert Go programmer
                with an assignment to create a Go program
                Create files in the default directory in $builder
                Always build the code to make sure it is valid
                Do not stop until your assignment is completed and the code builds
                Your assignment is: $assignment"""
            )
        )

        return work.env().output("completed").as_container()
