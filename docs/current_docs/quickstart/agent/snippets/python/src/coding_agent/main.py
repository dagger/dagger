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
        workspace = dag.toy_workspace()
        environment = (
            dag.env()
            .with_toy_workspace_input(
                "before", workspace, "tools to complete the assignment"
            )
            .with_string_input("assignment", assignment, "the assignment to complete")
            .with_toy_workspace_output("after", "the completed assignment")
        )

        return (
            dag.llm()
            .with_env(environment)
            .with_prompt(
                """
            You are an expert go programmer. You have access to a workspace.
            Use the default directory in the workspace.
            Do not stop until the code builds.
            Your assignment is: $assignment"""
            )
            .env()
            .output("after")
            .as_toy_workspace()
            .container()
        )
