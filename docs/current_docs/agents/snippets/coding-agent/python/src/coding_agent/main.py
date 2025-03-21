import dagger
from dagger import dag, function, object_type


@object_type
class CodingAgent:
    @function
    def go_program(self, assignment: str) -> dagger.Container:
        """Write a Go program"""
        result = (
            dag.llm()
            .with_toy_workspace(dag.toy_workspace())
            .with_prompt_var("assignment", assignment)
            .with_prompt("""
                You are an expert go programmer. You have access to a workspace.
                Use the default directory in the workspace.
                Do not stop until the code builds.
                Do not use the container.
                Complete the assignment: $assignment
                """)
            .toy_workspace()
            .container()
        )
        return result
