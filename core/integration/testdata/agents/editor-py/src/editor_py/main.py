"""A basic coding agent, for @agent integration tests."""

import dagger
from dagger import agent, dag, function, object_type


@object_type
class EditorPy:
    @function
    @agent
    def agent(self, base: dagger.LLM) -> dagger.LLM:
        """Compose the editor's tools onto a base LLM. Base named `base`."""
        return base.with_tools(dag.current_node()).with_system_prompt(
            "editor-py agent system prompt"
        )

    @function
    def read_file_py(self, path: str) -> str:
        """Read a file (stub)."""
        return f"read {path}"
